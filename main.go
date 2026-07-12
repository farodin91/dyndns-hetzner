package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/cruizba/publicip"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"go.yaml.in/yaml/v2"
)

const (
	DefaultTTL  = 180
	PublicIPTTL = 10
)

type Config struct {
	TTL     int      `yaml:"ttl"`
	Zone    string   `yaml:"zone"`
	Records []string `yaml:"records"`
}

func main() {
	apiToken := os.Getenv("HETZNER_DNS_API_TOKEN")
	if apiToken == "" {
		log.Fatal("HETZNER_DNS_API_TOKEN environment variable is required")
	}

	configPath := os.Getenv("HETZNER_DNS_CONFIG_PATH")
	if configPath == "" {
		configPath = "./config.yaml"
	}

	file, err := os.Open(configPath)
	if err != nil {
		log.Fatal("Open config failed", err)
	}
	yamlData, err := io.ReadAll(file)
	if err != nil {
		log.Fatal("Open config failed", err)
	}

	var config Config
	err = yaml.Unmarshal(yamlData, &config)
	if err != nil {
		log.Fatal("Parse config failed", err)
	}

	if config.Zone == "" {
		log.Fatal("zone variable is required")
	}

	if config.TTL == 0 {
		config.TTL = DefaultTTL
	}

	client := hcloud.NewClient(hcloud.WithToken(apiToken), hcloud.WithApplication("hetzner-dyndns", "1.0"))
	ctx := context.Background()

	publicIP, err := getOutboundIP(ctx)
	if err != nil {
		log.Fatalf("Failed to get public IP: %v", err)
	}
	log.Printf("Current public IP: %s", publicIP)
	if publicIP.To4() == nil {
		log.Fatal("Current IPv6 is unsupported")
	}

	// Get zone by name
	zone, _, err := client.Zone.GetByName(ctx, config.Zone)
	if err != nil {
		log.Fatalf("Failed to get zone: %v", err)
	}
	if zone == nil {
		log.Fatalf("Zone %s not found", config.Zone)
	}
	log.Printf("Found zone ID: %d for %s", zone.ID, zone.Name)

	for _, recordName := range config.Records {
		if recordName != "" {
			ensureRecord(ctx, client, zone, recordName, publicIP.String(), config.TTL)
		}
	}
}

func ensureRecord(
	ctx context.Context,
	client *hcloud.Client,
	zone *hcloud.Zone,
	rrsetName, publicIP string,
	ttl int,
) {
	rrset, resp, err := client.Zone.GetRRSetByNameAndType(ctx, zone, rrsetName, hcloud.ZoneRRSetTypeA)
	if err != nil {
		log.Fatalf("Failed to get RRSet: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Failed to get RRSet with Status %d", resp.StatusCode)
	}

	// Check if RRSet exists and has the same IP
	if rrset != nil && len(rrset.Records) > 0 {
		currentIP := rrset.Records[0]
		log.Printf("Current A record for %s in %s: %s", rrsetName, zone.Name, currentIP)

		if publicIP == currentIP.Value {
			log.Println("IP unchanged, no update needed")
			return
		}
		log.Printf("IP changed from %s to %s", currentIP.Value, publicIP)

		updateRecord(ctx, client, zone, rrsetName, publicIP, ttl)
	} else {
		createRecord(ctx, client, zone, rrsetName, publicIP, ttl)
	}
}

func updateRecord(
	ctx context.Context,
	client *hcloud.Client,
	zone *hcloud.Zone,
	rrsetName, publicIP string,
	ttl int,
) {
	log.Printf("A record for %s found, updating new RRSet with IP: %s", rrsetName, publicIP)
	_, resp, err := client.Zone.UpdateRRSet(ctx, &hcloud.ZoneRRSet{
		Zone: zone,
		Name: rrsetName,
		Type: hcloud.ZoneRRSetTypeA,
		TTL:  new(ttl),
		Records: []hcloud.ZoneRRSetRecord{
			{
				Value: publicIP,
			},
		},
	}, hcloud.ZoneRRSetUpdateOpts{})
	if err != nil {
		log.Fatalf("Failed to update RRSet: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Failed to update RRSet with Status %d", resp.StatusCode)
	}
	log.Println("Updated A record successfully")
}

func createRecord(
	ctx context.Context,
	client *hcloud.Client,
	zone *hcloud.Zone,
	rrsetName, publicIP string,
	ttl int,
) {
	log.Printf("A record for %s not found, creating new RRSet with IP: %s", rrsetName, publicIP)
	_, resp, err := client.Zone.CreateRRSet(ctx, zone, hcloud.ZoneRRSetCreateOpts{
		Name: rrsetName,
		Type: hcloud.ZoneRRSetTypeA,
		TTL:  new(ttl),
		Records: []hcloud.ZoneRRSetRecord{
			{
				Value: publicIP,
			},
		},
	})
	if err != nil {
		log.Fatalf("Failed to create RRSet: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Failed to create RRSet with Status %d", resp.StatusCode)
	}
	log.Println("Created A record successfully")
}

func getOutboundIP(ctx context.Context) (net.IP, error) {
	ctx, cancel := context.WithTimeout(ctx, PublicIPTTL*time.Second)
	defer cancel()
	client := publicip.NewClient()
	return client.Discover(ctx)
}
