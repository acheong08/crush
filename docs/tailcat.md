# Tailcat transport

Crush can expose its client/server API over [tailcat](https://github.com/tailscale/tailcat),
a WireGuard data plane with no coordination server and no account system. Peers
authenticate with node keys, so `crush server` can be reachable from a laptop or
phone without a public IP, a TLS certificate, or a VPN account.

## Server

```sh
crush server --host tailcat://
```

The command prints a `tailcat://` address on stdout and keeps running. The
address doubles as a capability: it embeds the server's WireGuard public key and
a pre-shared key. Hand it to a client and that client can connect.

By default the tunnel listens on port 8080. Override it with
`--tailcat-port` or `tailcat://:9000`.

Server identity is persisted in `tailcat-server.json` next to `crush.json`
(`$XDG_DATA_HOME/crush/` by default, or `$CRUSH_GLOBAL_DATA`), so the printed
address stays valid across restarts.

## Clients

```sh
crush --host 'tailcat://<address from the server>'
```

A `tailcat://` host implies client/server mode; `CRUSH_CLIENT_SERVER=1` is not
required.

Client identity is persisted in `tailcat-client.json` in the same directory. The
key is stable per machine, which is what makes allowlisting possible.

## Allowlisting devices

Without an allowlist, any peer that holds the address can connect and drive
tools. Lock it down to your own devices:

```sh
# On each client machine, once:
crush tailcat id
# nodekey:5539e228...

# On the server:
crush server --host tailcat:// \
  --allow-peer nodekey:5539e228...,nodekey:aa11bb22...
```

`--allow-peer` is repeatable and also accepts a comma-separated list. The same
list can be supplied through `CRUSH_TAILCAT_ALLOW`. Unknown peers are ignored at
the tunnel layer, so a rejected client hangs rather than receiving an error.

## Caveats

- tailcat has no API or wire-format stability promises yet.
- The default DERP relays belong to Tailscale, are rate limited, and carry no
  uptime guarantee. Run your own DERP server and embed it in the address if you
  depend on this transport.
- The address is a bearer capability. Treat it like a password even when an
  allowlist is configured.
- There are no ACLs or user identities. Authorization is whatever the server's
  API already enforces, so run one server per trust boundary.
