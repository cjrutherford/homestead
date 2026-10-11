# Tunnel Remote Access

## Status
Accepted

## Decision
Cloudflare Tunnel joins the remote-access providers from ADR-011 as the
preferred path when the local router is silent or the uplink sits behind
carrier-grade NAT: discovery tries UPnP, NAT-PMP, and PCP first and falls
through to tunnel setup, which runs an outbound-only connector on the
appliance and serves customer hostnames from the provider edge. Router
mappings remain fully supported where available; the tunnel never opens
inbound appliance ports and never replaces the firewall boundary.

## Context
Starlink-class uplinks (CGNAT, no router access, no UPnP answers) cannot
port-forward at all. The tunnel path was proven live against Cloudflare
with zero router cooperation: DNS stays the source of truth, credentials
stay sealed, and all traffic arrives over loopback from the connector.

## Consequences
- Implementation must preserve this boundary.
- Changes require a superseding ADR.
