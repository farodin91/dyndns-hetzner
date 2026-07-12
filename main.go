package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"os"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"go.yaml.in/yaml/v2"
)

const DefaultTTL = 180

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

	// Create Hetzner client
	client := hcloud.NewClient(hcloud.WithToken(apiToken), hcloud.WithApplication("hetzner-dyndns", "1.0"))
	ctx := context.Background()

	// Get current public IP
	publicIP, err := getOutboundIP()
	if err != nil {
		log.Fatalf("Failed to get public IP: %v", err)
	}
	log.Printf("Current public IP: %s", publicIP)

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
	rrset, _, err := client.Zone.GetRRSetByNameAndType(ctx, zone, rrsetName, hcloud.ZoneRRSetTypeA)
	if err != nil {
		log.Fatalf("Failed to get RRSet: %v", err)
	}

	// Check if RRSet exists and has the same IP
	if rrset != nil && len(rrset.Records) > 0 {
		currentIP := rrset.Records[0]
		log.Printf("Current A record for %s in %s: %s", rrsetName, zone.Name, currentIP)

		if publicIP == currentIP.Value {
			log.Println("IP unchanged, no update needed")
			return
		}
		log.Printf("IP changed from %s to %s", currentIP, publicIP)

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
	// Update the RRSet
	log.Printf("A record for %s not found, creating new RRSet with IP: %s", rrsetName, publicIP)
	_, _, err := client.Zone.UpdateRRSet(ctx, &hcloud.ZoneRRSet{
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
	log.Println("Updated A record successfully")
}

func createRecord(
	ctx context.Context,
	client *hcloud.Client,
	zone *hcloud.Zone,
	rrsetName, publicIP string,
	ttl int,
) {
	// RRSet doesn't exist, create it
	log.Printf("A record for %s not found, creating new RRSet with IP: %s", rrsetName, publicIP)
	_, _, err := client.Zone.CreateRRSet(ctx, zone, hcloud.ZoneRRSetCreateOpts{
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
	log.Println("Created A record successfully")
}

// Get preferred outbound ip of this machine
func getOutboundIP() (*net.IP, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return nil, errors.New("expect UDPAddr")
	}

	return &localAddr.IP, nil
}
