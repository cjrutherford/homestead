# Tunnel Teardown Restores the Records Its Setup Replaced

## Status
Accepted

Owner decision 2026-10-20, from the S294 review. Amends ADR-037 §3
(Ownership and Teardown): replaced records are put back instead of being
left deleted. The rest of ADR-037 is unchanged.

## Decision
- **At setup**, before the DNS step deletes a record the approved plan
  replaces (an A, AAAA or CNAME at the tunnel hostname), Homestead records
  it: tunnel, zone, record ID, type, name, content, proxy flag and TTL
  (table `tunnel_replaced_records`). Nothing secret is stored.
- **When setup fails** after deleting records, the undo deletes the CNAME
  it created, puts the replaced records back, then deletes the tunnel.
- **At teardown**, after the owned CNAME is deleted, a `restore-dns` step
  puts each replaced record back as it was, with a comment saying Homestead
  restored it.
- **Nothing is overwritten.** If the name holds an A, AAAA or CNAME record
  again when teardown runs (the owner's own, or a Homestead CNAME kept
  because the owner changed it), the replaced records for that name are not
  restored. The skip is audited as `tunnel.teardown.dns-not-restored`.
- The plan's replace warning and the teardown confirmation say this.

## Context
ADR-037 left replaced records deleted after teardown. In the S294 review
the owner asked for teardown to undo setup's DNS change, so removing a
tunnel returns the hostname to what it served before.

## Alternatives considered
- **Leave them deleted (ADR-037 as written):** simple, but the owner must
  recreate records by hand after a teardown.
- **Restore over whatever is there:** could destroy a record the owner made
  after setup; refused under the ownership rule.

## Consequences
- Teardown gains one step (`restore-dns`), and migration 46 adds the table.
- Restored records get new Cloudflare IDs. The record comment is not kept,
  and Homestead does not own the restored record.
- Tunnels set up before this change have no replaced records on file, so
  nothing is restored for them.
