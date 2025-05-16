# Tailflare DNS

Updates Cloudflare DNS records with IP addresses of Tailscale servers, allowing for public aliases of servers in a Tailscale network.

When:

- you want to run TLS for your Tailscale servers
- you don't want to run separate (private) DNS
- you're happy with the server IPs being public
- you own a domain you can delegate to

... then this might be useful to you.

You might just want to use Terraform to do it. The purpose of this project is to run in AWS (free tier compatible) connected to a Tailscale webhook. When a Tailscale server is updated, the update should flow through and update the Cloudflare DNS zone.

It works straight from the command line though.

## Configuration

## Environment

This project uses direnv for local execution.

Create a file `.envrc.private` and copy the environment variables from `.envrc`. The following instructions fill them in.

### Tailscale

Create an OAuth app that has `devices:core:read` for the Tailnet your servers are joined to. Fill the OAuth client ID and secret in `.envrc.private` and update the Tailnet name too.

### Cloudflare

Create an API token with access to the zone that will be updated. Fill this in in `.envrc.private`.

### Server aliases

Create a file called `config.yaml` in the current directory with the following structure:

```yaml
# the name of the Cloudflare zone
zone: example.com

# alias names to create in Cloudflare
aliases:
  home.example.com: # note the suffix with the zone name, this is required or updates will fail
    - picluster1 # map to a single Tailscale server
  reader.home.example.com:
    - picluster1 # map to a pair of Tailscale servers
    - picluster2
```

## Running locally

1. Execute `devbox install`
2. Execute `go run main.go`
