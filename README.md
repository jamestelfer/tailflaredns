# Tailflare DNS

Updates Cloudflare DNS records with IP addresses of Tailscale servers, allowing for public aliases of servers in a Tailscale network.

If you don't want to run a separate private DNS, you're happy with the TS server IPs being public, and you can delegate a zone to Cloudflare DNS, then this might work for you.

This is designed to be added as a reactor to a webhook, but for now it's command line only.
