package main

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"os"
	"slices"

	"github.com/cloudflare/cloudflare-go/v4"
	"github.com/cloudflare/cloudflare-go/v4/dns"
	"github.com/cloudflare/cloudflare-go/v4/packages/pagination"
	"github.com/cloudflare/cloudflare-go/v4/zones"
	"github.com/jamestelfer/tailflaredns/seq"
	"gopkg.in/yaml.v2"
	"tailscale.com/client/tailscale/v2"
)

type Config struct {
	ZoneName string              `yaml:"zone"`
	Aliases  map[string][]string `yaml:"aliases"`
}

func parseConfig() (Config, error) {
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("failed to unmarshal config file: %w", err)
	}

	return config, nil
}

type ARecord struct {
	Id            string
	Address       string
	TailscaleName string
}

func (a ARecord) String() string {
	return fmt.Sprintf("%s (%s)", a.Address, a.TailscaleName)
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if err := run(); err != nil {
		slog.Error("Update process failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseConfig()
	if err != nil {
		return err
	}

	ctx := context.Background()
	devices, err := getTailscaleDevices(ctx)
	if err != nil {
		return fmt.Errorf("failed to get Tailscale devices: %w", err)
	}
	failures := []error{}

	successful := 0
	failed := 0

	mgr := NewDNSManager(cfg.ZoneName)

	for alias, servers := range cfg.Aliases {
		aliasRecords := make([]ARecord, 0, len(servers))

		for _, serverName := range servers {
			record, ok := devices[serverName]
			if !ok {
				// treat this as a failure but non fatal. It just means that the given
				// server won't be mapped for this alias.
				failures = append(failures, fmt.Errorf("(alias %s) Tailscale server '%s' not found: %w", alias, serverName, err))
				continue
			}
			aliasRecords = append(aliasRecords, record)
		}

		slog.Info("Updating alias", "alias", alias, "servers", aliasRecords)
		if err := updateCloudflareRecord(ctx, mgr, alias, aliasRecords); err != nil {
			failed++
			failures = append(failures, fmt.Errorf("(alias %s) failed to update Cloudflare record: %w", alias, err))
			continue
		}

		successful++
	}

	slog.Info("Alias update complete", "successful", successful, "failed", failed)

	if len(failures) > 0 {
		return errors.Join(failures...)
	}

	return nil
}

// getTailscaleDevices retrieves all Tailscale devices and their IP addresses.
// This doesn't scale with large networks. Fortunately, mine isn't large.
func getTailscaleDevices(ctx context.Context) (map[string]ARecord, error) {
	client := &tailscale.Client{
		Tailnet: os.Getenv("TAILSCALE_TAILNET"),
		HTTP: tailscale.OAuthConfig{
			ClientID:     os.Getenv("TAILSCALE_OAUTH_CLIENT_ID"),
			ClientSecret: os.Getenv("TAILSCALE_OAUTH_CLIENT_SECRET"),
			// this is the maximum necessary permissions for this operation
			Scopes: []string{"devices:core:read"},
		}.HTTPClient(),
	}

	devices, err := client.Devices().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tailscale devices: %w", err)
	}

	// Create a map of device names to IP addresses.
	records := seq.ToMap(
		seq.Select(
			slices.Values(devices),
			func(d tailscale.Device) ARecord {
				return ARecord{
					TailscaleName: d.Hostname,
					Address:       d.Addresses[0],
				}
			},
		),
		func(r ARecord) string { return r.TailscaleName },
	)

	return records, nil
}

// updateCloudflareRecord creates or updates an A record in Cloudflare.
// This is just a placeholder function.
func updateCloudflareRecord(ctx context.Context, mgr *DNSManager, alias string, addresses []ARecord) error {
	err := mgr.UpdateAddressesByName(ctx, alias, addresses)
	if err != nil {
		return fmt.Errorf("failed to update records for alias %s: %w", alias, err)
	}

	return nil
}

type DNSManager struct {
	cfClient *cloudflare.Client
	zoneName string
	zoneID   *string
}

func NewDNSManager(zoneName string) *DNSManager {
	client := cloudflare.NewClient()

	return &DNSManager{
		cfClient: client,
		zoneName: zoneName,
	}
}

func (d *DNSManager) getZone(ctx context.Context) (string, error) {
	if d.zoneID != nil {
		return *d.zoneID, nil
	}

	zones, err := d.cfClient.Zones.List(ctx, zones.ZoneListParams{
		Name: cloudflare.String(d.zoneName),
	})
	if err != nil {
		return "", fmt.Errorf("zone lookup for name %s failed: %w", d.zoneName, err)
	}
	if len(zones.Result) == 0 {
		return "", fmt.Errorf("zone %s not found", d.zoneName)
	}

	// cache the zone ID for future use
	zoneID := zones.Result[0].ID
	d.zoneID = &zoneID

	return zoneID, nil
}

// GetAddressesByName retrieves the IP addresses associated with the A records
// for a given DNS record name. This name should be "@" for the root records.
// An empty slice is returned if no records are found.
func (d *DNSManager) GetAddressesByName(ctx context.Context, name string) ([]ARecord, error) {
	zoneID, err := d.getZone(ctx)
	if err != nil {
		return nil, err
	}

	pager := d.cfClient.DNS.Records.ListAutoPaging(ctx, dns.RecordListParams{
		// all A records in this zone with the given name
		ZoneID: cloudflare.String(zoneID),
		Match:  cloudflare.F(dns.RecordListParamsMatchAll),
		Name: cloudflare.F(dns.RecordListParamsName{
			Exact: cloudflare.String(name),
		}),
		Type: cloudflare.F(dns.RecordListParamsTypeA),
	})

	addresses := []ARecord{}

	for record := range ranger(pager) {
		addresses = append(addresses, ARecord{
			Id:            record.ID,
			Address:       record.Content,
			TailscaleName: record.Name,
		})
	}
	if pager.Err() != nil {
		return nil, fmt.Errorf("failed to list records for name %s: %w", name, pager.Err())
	}

	return addresses, nil
}

func (d *DNSManager) UpdateAddressesByName(ctx context.Context, name string, addresses []ARecord) error {
	zoneID, err := d.getZone(ctx)
	if err != nil {
		return err
	}

	// get the set of currentAddresses records for this name
	currentAddresses, err := d.GetAddressesByName(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to get current addresses for name %s: %w", name, err)
	}

	// compare the existing records with the new addresses and create 3 sets: create, update, and delete
	create, update, delete := crud(currentAddresses, addresses, func(a ARecord) string { return a.Address })

	// create a batch of changes to apply
	batch := dns.RecordBatchParams{
		ZoneID: cloudflare.String(zoneID),
		Posts: cloudflare.F(seq.SelectSlice(create, func(a ARecord) dns.RecordUnionParam {
			return dns.ARecordParam{
				Type:    cloudflare.F(dns.ARecordTypeA),
				Name:    cloudflare.String(name),
				Content: cloudflare.String(a.Address),
				Comment: cloudflare.String(fmt.Sprintf("Address of Tailscale server '%s'", a.TailscaleName)),
				TTL:     cloudflare.F(dns.TTL1),
				Proxied: cloudflare.Bool(false),
			}
		})),
		Patches: cloudflare.F(seq.SelectSlice(update, func(a ARecord) dns.BatchPatchUnionParam {
			return dns.BatchPatchAParam{
				ID: cloudflare.String(a.Id),
				ARecordParam: dns.ARecordParam{
					Type:    cloudflare.F(dns.ARecordTypeA),
					TTL:     cloudflare.F(dns.TTL1),
					Proxied: cloudflare.Bool(false),
				},
			}
		})),
		Deletes: cloudflare.F(seq.SelectSlice(delete, func(a ARecord) dns.RecordBatchParamsDelete {
			return dns.RecordBatchParamsDelete{
				ID: cloudflare.String(a.Id),
			}
		})),
	}

	// execute the batch of changes using the Cloudflare API batch method
	_, err = d.cfClient.DNS.Records.Batch(ctx, batch)

	return err
}

func ranger[T any](pager *pagination.V4PagePaginationArrayAutoPager[T]) iter.Seq[T] {
	return func(yield func(T) bool) {
		for pager.Next() {
			if !yield(pager.Current()) {
				return
			}
		}
	}
}

func crud[T any, K comparable](current []T, after []T, keyFunc func(T) K) (create, update, delete []T) {
	// create a set of existing records
	existingSet := make(map[K]T, len(current))
	for _, c := range current {
		existingSet[keyFunc(c)] = c
	}

	// create the set of "after" records
	afterSet := make(map[K]T, len(after))
	for _, e := range after {
		afterSet[keyFunc(e)] = e
	}

	// find records to create or update
	for key, val := range afterSet {
		if _, exists := existingSet[key]; exists {
		} else {
			create = append(create, val)
		}
	}

	// find records to delete or update -- existing records will have their ID data set
	for key, val := range existingSet {
		if _, exists := afterSet[key]; exists {
			update = append(update, val)
		} else {
			delete = append(delete, val)
		}
	}

	return create, update, delete
}
