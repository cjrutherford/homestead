package router

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"homestead/internal/store"
)

// TunnelTarget is the name a tunnel's public hostnames point at: Cloudflare
// routes a proxied CNAME to it into the tunnel (S294, ADR-037).
func TunnelTarget(tunnelID string) string {
	return tunnelID + ".cfargotunnel.com"
}

// Zone is a Cloudflare zone the token can see, with the account that owns
// it. The account is how the tunnel finds its tenancy without asking the
// owner for an account ID (ADR-037).
type Zone struct {
	ID        string
	Name      string
	AccountID string
}

// ErrNoZone means no zone the token can see contains the hostname.
var ErrNoZone = errors.New("router: no zone contains the hostname")

// ZoneFor finds the longest zone containing hostname: app.example.com is
// looked up as app.example.com, then example.com. Each lookup is an exact
// zone-name query, so no listing of every zone is needed.
func (c *TunnelClient) ZoneFor(ctx context.Context, token, hostname string) (Zone, error) {
	labels := strings.Split(hostname, ".")
	for i := 0; i+2 <= len(labels); i++ {
		candidate := strings.Join(labels[i:], ".")
		var zones []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Account struct {
				ID string `json:"id"`
			} `json:"account"`
		}
		if err := c.call(ctx, token, http.MethodGet, "/zones?name="+url.QueryEscape(candidate), nil, &zones); err != nil {
			return Zone{}, err
		}
		for _, zone := range zones {
			if zone.Name == candidate && zone.ID != "" {
				return Zone{ID: zone.ID, Name: zone.Name, AccountID: zone.Account.ID}, nil
			}
		}
	}
	return Zone{}, ErrNoZone
}

// DNSRecord is one record in a zone, as the tunnel plan needs it.
type DNSRecord struct {
	ID      string `json:"-"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
	// Comment is the owner's note on the record, kept so a restored
	// record carries it back.
	Comment string `json:"-"`
}

type cloudflareRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
	Comment string `json:"comment"`
}

func (r cloudflareRecord) record() DNSRecord {
	return DNSRecord{ID: r.ID, Type: r.Type, Name: r.Name, Content: r.Content, Proxied: r.Proxied, TTL: r.TTL, Comment: r.Comment}
}

func validateZoneID(zoneID string) error {
	if zoneID == "" || strings.ContainsAny(zoneID, "/?#") {
		return fmt.Errorf("router: invalid zone id")
	}
	return nil
}

// RecordsAt lists every record named exactly hostname in the zone.
func (c *TunnelClient) RecordsAt(ctx context.Context, token, zoneID, hostname string) ([]DNSRecord, error) {
	if err := validateZoneID(zoneID); err != nil {
		return nil, err
	}
	var records []cloudflareRecord
	path := "/zones/" + zoneID + "/dns_records?per_page=100&name=" + url.QueryEscape(hostname)
	if err := c.call(ctx, token, http.MethodGet, path, nil, &records); err != nil {
		return nil, err
	}
	out := []DNSRecord{}
	for _, record := range records {
		if record.Name == hostname {
			out = append(out, record.record())
		}
	}
	return out, nil
}

// GetRecord reads one record by ID. A refusal means it is gone.
func (c *TunnelClient) GetRecord(ctx context.Context, token, zoneID, recordID string) (DNSRecord, error) {
	if err := validateZoneID(zoneID); err != nil {
		return DNSRecord{}, err
	}
	if recordID == "" || strings.ContainsAny(recordID, "/?#") {
		return DNSRecord{}, fmt.Errorf("router: invalid record id")
	}
	var record cloudflareRecord
	if err := c.call(ctx, token, http.MethodGet, "/zones/"+zoneID+"/dns_records/"+recordID, nil, &record); err != nil {
		return DNSRecord{}, err
	}
	return record.record(), nil
}

// CreateTunnelCNAME points hostname at the tunnel: a proxied CNAME (a
// tunnel only answers through Cloudflare's proxy), with a comment saying
// Homestead made it.
func (c *TunnelClient) CreateTunnelCNAME(ctx context.Context, token, zoneID, hostname, tunnelID string) (DNSRecord, error) {
	if err := validateZoneID(zoneID); err != nil {
		return DNSRecord{}, err
	}
	if tunnelID == "" {
		return DNSRecord{}, fmt.Errorf("router: tunnel id must not be empty")
	}
	var created cloudflareRecord
	body := map[string]any{
		"type":    "CNAME",
		"name":    hostname,
		"content": TunnelTarget(tunnelID),
		"proxied": true,
		"ttl":     1,
		"comment": "Created by Homestead for its tunnel",
	}
	if err := c.call(ctx, token, http.MethodPost, "/zones/"+zoneID+"/dns_records", body, &created); err != nil {
		return DNSRecord{}, err
	}
	if created.ID == "" {
		return DNSRecord{}, fmt.Errorf("router: record create returned no id")
	}
	return created.record(), nil
}

// RestoreRecord recreates a record a tunnel replaced, as it was: type,
// name, content, proxy, TTL and comment. A record that had no comment
// gets one saying Homestead put it back.
func (c *TunnelClient) RestoreRecord(ctx context.Context, token, zoneID string, record DNSRecord) (DNSRecord, error) {
	if err := validateZoneID(zoneID); err != nil {
		return DNSRecord{}, err
	}
	ttl := record.TTL
	if ttl == 0 {
		ttl = 1
	}
	comment := record.Comment
	if comment == "" {
		comment = "Restored by Homestead when its tunnel was removed"
	}
	var created cloudflareRecord
	body := map[string]any{
		"type":    record.Type,
		"name":    record.Name,
		"content": record.Content,
		"proxied": record.Proxied,
		"ttl":     ttl,
		"comment": comment,
	}
	if err := c.call(ctx, token, http.MethodPost, "/zones/"+zoneID+"/dns_records", body, &created); err != nil {
		return DNSRecord{}, err
	}
	return created.record(), nil
}

// DeleteRecord deletes one record by ID.
func (c *TunnelClient) DeleteRecord(ctx context.Context, token, zoneID, recordID string) error {
	if err := validateZoneID(zoneID); err != nil {
		return err
	}
	if recordID == "" || strings.ContainsAny(recordID, "/?#") {
		return fmt.Errorf("router: invalid record id")
	}
	return c.call(ctx, token, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+recordID, nil, nil)
}

// OwnedRecord is a DNS record Homestead created for a tunnel. Teardown
// deletes only these (ADR-037).
type OwnedRecord struct {
	RecordID string
	ZoneID   string
	Hostname string
	TunnelID string
}

// RecordOwned remembers a record Homestead created.
func RecordOwned(st *store.Store, record OwnedRecord) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB().Exec(
		`INSERT OR REPLACE INTO tunnel_dns_records (record_id, zone_id, hostname, tunnel_id, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		record.RecordID, record.ZoneID, record.Hostname, record.TunnelID, now,
	); err != nil {
		return fmt.Errorf("router: record owned dns: %w", err)
	}
	return nil
}

// OwnedRecords lists the records Homestead created for a tunnel.
func OwnedRecords(st *store.Store, tunnelID string) ([]OwnedRecord, error) {
	rows, err := st.DB().Query(
		`SELECT record_id, zone_id, hostname, tunnel_id FROM tunnel_dns_records
		 WHERE tunnel_id = ? ORDER BY hostname`, tunnelID)
	if err != nil {
		return nil, fmt.Errorf("router: list owned dns: %w", err)
	}
	defer rows.Close()
	out := []OwnedRecord{}
	for rows.Next() {
		var record OwnedRecord
		if err := rows.Scan(&record.RecordID, &record.ZoneID, &record.Hostname, &record.TunnelID); err != nil {
			return nil, fmt.Errorf("router: scan owned dns: %w", err)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// ForgetOwned drops a record from Homestead's list.
func ForgetOwned(st *store.Store, recordID string) error {
	if _, err := st.DB().Exec(`DELETE FROM tunnel_dns_records WHERE record_id = ?`, recordID); err != nil {
		return fmt.Errorf("router: forget owned dns: %w", err)
	}
	return nil
}

// ReplacedRecord is a record a tunnel's setup deleted to make room for
// its CNAME. Teardown puts it back (S294).
type ReplacedRecord struct {
	TunnelID string
	ZoneID   string
	Record   DNSRecord
}

// RecordReplaced remembers a record setup deleted, before it is deleted.
func RecordReplaced(st *store.Store, replaced ReplacedRecord) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r := replaced.Record
	proxied := 0
	if r.Proxied {
		proxied = 1
	}
	if _, err := st.DB().Exec(
		`INSERT OR REPLACE INTO tunnel_replaced_records (record_id, tunnel_id, zone_id, type, name, content, proxied, ttl, comment, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, replaced.TunnelID, replaced.ZoneID, r.Type, r.Name, r.Content, proxied, r.TTL, r.Comment, now,
	); err != nil {
		return fmt.Errorf("router: record replaced dns: %w", err)
	}
	return nil
}

// ReplacedRecords lists the records a tunnel's setup deleted.
func ReplacedRecords(st *store.Store, tunnelID string) ([]ReplacedRecord, error) {
	rows, err := st.DB().Query(
		`SELECT record_id, tunnel_id, zone_id, type, name, content, proxied, ttl, comment FROM tunnel_replaced_records
		 WHERE tunnel_id = ? ORDER BY name, record_id`, tunnelID)
	if err != nil {
		return nil, fmt.Errorf("router: list replaced dns: %w", err)
	}
	defer rows.Close()
	out := []ReplacedRecord{}
	for rows.Next() {
		var r ReplacedRecord
		var proxied int
		if err := rows.Scan(&r.Record.ID, &r.TunnelID, &r.ZoneID, &r.Record.Type, &r.Record.Name,
			&r.Record.Content, &proxied, &r.Record.TTL, &r.Record.Comment); err != nil {
			return nil, fmt.Errorf("router: scan replaced dns: %w", err)
		}
		r.Record.Proxied = proxied != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ForgetReplaced drops a replaced record once it is restored or kept out.
func ForgetReplaced(st *store.Store, tunnelID, recordID string) error {
	if _, err := st.DB().Exec(`DELETE FROM tunnel_replaced_records WHERE tunnel_id = ? AND record_id = ?`, tunnelID, recordID); err != nil {
		return fmt.Errorf("router: forget replaced dns: %w", err)
	}
	return nil
}

// SetTunnelAccount stores the account and zone a tunnel's routes were
// created under, for status and teardown. They never leave the store.
func SetTunnelAccount(st *store.Store, tunnelID, accountID, zoneID string) error {
	if _, err := st.DB().Exec(
		`UPDATE tunnel_routes SET account_id = ?, zone_id = ? WHERE tunnel_id = ?`,
		accountID, zoneID, tunnelID,
	); err != nil {
		return fmt.Errorf("router: set tunnel account: %w", err)
	}
	return nil
}

// TunnelAccount returns the stored account for a tunnel, empty when the
// tunnel was set up before S294.
func TunnelAccount(st *store.Store, tunnelID string) (string, error) {
	var account string
	err := st.DB().QueryRow(
		`SELECT account_id FROM tunnel_routes WHERE tunnel_id = ? AND account_id != '' LIMIT 1`, tunnelID,
	).Scan(&account)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("router: tunnel account: %w", err)
	}
	return account, nil
}

// TunnelZone returns the stored zone a tunnel's routes live in, empty
// when the tunnel was set up before S294.
func TunnelZone(st *store.Store, tunnelID string) (string, error) {
	var zone string
	err := st.DB().QueryRow(
		`SELECT zone_id FROM tunnel_routes WHERE tunnel_id = ? AND zone_id != '' LIMIT 1`, tunnelID,
	).Scan(&zone)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("router: tunnel zone: %w", err)
	}
	return zone, nil
}
