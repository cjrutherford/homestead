package catalog

import (
	"fmt"
	"sort"
	"strings"

	"homestead/internal/storage"
)

// Catalog bundles (S344, F5). S172 graded the recommended suite one
// package at a time; a bundle answers the owner's actual question: "if I
// install this set, does the appliance hold it?" Membership is curated
// intent, not a promise that every package ships today — waves fill the
// lists in, and a member the catalog does not yet carry is reported as
// missing instead of silently dropped.

// Bundle is one curated profile.
type Bundle struct {
	// Name is the stable identifier used by the API and CLI.
	Name  string
	Title string
	Blurb string
	// Packages is curated install order; it is never derived from a
	// directory listing, so a package joins a bundle deliberately.
	Packages []string
}

// Bundles lists the profiles in display order: smallest commitment
// first, everything last. Every profile includes the starter set, so an
// owner can grow a homestead without starting over.
var Bundles = []Bundle{
	{
		Name:  "starter",
		Title: "Starter",
		Blurb: "Files, media and passwords on a modest appliance.",
		Packages: []string{
			"nextcloud", "jellyfin", "vaultwarden", "linkding", "freshrss", "uptime-kuma",
		},
	},
	{
		Name:  "family",
		Title: "Family",
		Blurb: "The starter set plus photos, documents, books and shared planning.",
		Packages: []string{
			"nextcloud", "jellyfin", "vaultwarden", "linkding", "freshrss", "uptime-kuma",
			"immich", "paperless-ngx", "mealie", "vikunja", "actual-budget",
			"audiobookshelf", "navidrome", "gitea", "memos",
		},
	},
	{
		Name:  "media",
		Title: "Media",
		Blurb: "Playback plus automated library management and reading.",
		Packages: []string{
			"nextcloud", "jellyfin", "vaultwarden", "linkding", "freshrss", "uptime-kuma",
			"prowlarr", "sonarr", "radarr", "sabnzbd", "seerr", "komga", "calibre-web-automated",
		},
	},
	{
		Name:  "full",
		Title: "Full",
		Blurb: "Everything curated, without the optional torrent client.",
		Packages: []string{
			"nextcloud", "jellyfin", "vaultwarden", "linkding", "freshrss", "uptime-kuma",
			"immich", "paperless-ngx", "mealie", "vikunja", "actual-budget",
			"audiobookshelf", "navidrome", "gitea", "memos",
			"prowlarr", "sonarr", "radarr", "sabnzbd", "seerr", "komga",
			"calibre-web-automated", "bookstack",
		},
	},
}

// Validate checks a curated bundle: identity, a non-empty package list,
// unique members, and no blank names. It says nothing about availability;
// that is the catalog's business at check time.
func (b Bundle) Validate() error {
	if b.Name == "" {
		return fmt.Errorf("catalog: bundle name must not be empty")
	}
	if strings.ContainsAny(b.Name, " \t\r\n/") {
		return fmt.Errorf("catalog: bundle name %q must be a single word", b.Name)
	}
	if b.Title == "" {
		return fmt.Errorf("catalog: bundle %q needs a title", b.Name)
	}
	if len(b.Packages) == 0 {
		return fmt.Errorf("catalog: bundle %q is empty", b.Name)
	}
	seen := map[string]bool{}
	for _, name := range b.Packages {
		if name == "" {
			return fmt.Errorf("catalog: bundle %q has a blank package", b.Name)
		}
		if seen[name] {
			return fmt.Errorf("catalog: bundle %q lists %q twice", b.Name, name)
		}
		seen[name] = true
	}
	return nil
}

// BundleMember is one curated package's availability and declared
// memory floor.
type BundleMember struct {
	Name      string
	Available bool
	MemoryMB  uint64
}

// BundleStorage is one storage class's combined demand across the
// bundle's available members. Known is false when the host has no
// assignment for the class, which fails the check rather than passing
// on missing data.
type BundleStorage struct {
	Class  storage.Class
	Needed uint64
	Free   uint64
	Known  bool
	OK     bool
}

// BundleReport grades one bundle against one host: combined memory,
// summed per-class storage, and the members the catalog does not carry
// yet. Complete is informational — an incomplete bundle still grades the
// packages that exist today.
type BundleReport struct {
	Bundle   string
	Title    string
	Blurb    string
	Members  []BundleMember
	Missing  []string
	Complete bool

	TotalMemory uint64
	HostMemory  uint64
	Storage     []BundleStorage

	Passed bool
	Detail string
}

// CheckBundle grades a bundle on one host. Capacity combines: the sum of
// member memory floors must fit, and each storage class's summed floor
// must fit that class's free space. Per-package verdicts, ports and
// exposure stay install-time concerns — a bundle preview answers "does
// this set fit", not "is every package installable right now". Ports are
// container ports on separate networks and routes never publish them, so
// two members listening on 80 is normal, not a collision.
func CheckBundle(cat Catalog, bundle Bundle, host HostFacts) (BundleReport, error) {
	if err := bundle.Validate(); err != nil {
		return BundleReport{}, err
	}
	report := BundleReport{
		Bundle: bundle.Name, Title: bundle.Title, Blurb: bundle.Blurb,
		HostMemory: host.MemoryMB,
		// Non-nil so the JSON carries [] not null; the dashboard reads
		// .length on these lists.
		Members: []BundleMember{},
		Missing: []string{},
		Storage: []BundleStorage{},
	}
	needed := map[storage.Class]uint64{}
	available := 0
	for _, name := range bundle.Packages {
		manifest, err := cat.Get(name)
		if err != nil {
			report.Missing = append(report.Missing, name)
			report.Members = append(report.Members, BundleMember{Name: name})
			continue
		}
		if err := manifest.Validate(); err != nil {
			return BundleReport{}, fmt.Errorf("catalog: bundle %q member %q: %w", bundle.Name, name, err)
		}
		available++
		report.Members = append(report.Members, BundleMember{
			Name: name, Available: true, MemoryMB: manifest.Requirements.MemoryMB,
		})
		report.TotalMemory += manifest.Requirements.MemoryMB
		for _, need := range manifest.Requirements.Storage {
			needed[need.Class] += need.MinBytes
		}
	}
	report.Complete = len(report.Missing) == 0

	var failures []string
	if available == 0 {
		failures = append(failures, "no bundle members are in the catalog yet")
	}
	if host.MemoryMB == 0 {
		failures = append(failures, "no memory data for the host")
	} else if report.TotalMemory > host.MemoryMB {
		failures = append(failures, fmt.Sprintf(
			"combined memory %d MB exceeds host %d MB", report.TotalMemory, host.MemoryMB))
	}
	classes := make([]storage.Class, 0, len(needed))
	for class := range needed {
		classes = append(classes, class)
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i] < classes[j] })
	for _, class := range classes {
		free, known := host.FreeSpace[class]
		entry := BundleStorage{Class: class, Needed: needed[class], Free: free, Known: known}
		entry.OK = known && free >= entry.Needed
		if !known {
			failures = append(failures, fmt.Sprintf("no space data for %s storage", class))
		} else if !entry.OK {
			failures = append(failures, fmt.Sprintf(
				"%s storage needs %d bytes, host has %d", class, entry.Needed, free))
		}
		report.Storage = append(report.Storage, entry)
	}

	if len(failures) > 0 {
		report.Detail = "bundle blocked: " + strings.Join(failures, "; ")
		return report, nil
	}
	report.Passed = true
	report.Detail = fmt.Sprintf("bundle fits: %d of %d packages available, %d MB of %d MB",
		available, len(bundle.Packages), report.TotalMemory, host.MemoryMB)
	return report, nil
}
