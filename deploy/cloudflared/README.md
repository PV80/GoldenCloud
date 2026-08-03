# Cloudflare Tunnel

This folder holds the tunnel configuration for GoldenCloud. The step-by-step
setup lives in [`../RUNBOOK.md`](../RUNBOOK.md) — this page is the short
explanation of what the tunnel is and how to check it is healthy.

## What the tunnel is

`cloudflared` is a small program that runs on the same machine as the
GoldenCloud server. It makes an **outbound** connection to Cloudflare and holds
it open. When a staff member's PC asks for `https://cloud.example.com`,
Cloudflare receives that request and pushes it down the connection the tunnel
already opened. `cloudflared` then hands it to the GoldenCloud server on
`http://127.0.0.1:8080`, and the answer goes back the same way.

```
Staff PC ──HTTPS──▶ Cloudflare edge ◀──outbound connection── cloudflared ──▶ 127.0.0.1:8080
                                        (opened from your office,             goldencloud
                                         never accepted into it)
```

## Why it needs no open ports

Nothing on the internet ever connects **to** your office. Your office connects
**out**, the same way a web browser or an email client does, and the answers
come back on that same connection.

That is why:

- **No port forwarding.** There is no inbound port to forward. Your router's
  configuration is untouched.
- **No firewall holes.** Outbound HTTPS on port 443 is already allowed on
  essentially every network, because otherwise nothing would work.
- **It works behind carrier-grade NAT.** If your ISP does not give you a real
  public IP address — common on 4G, 5G, Starlink, and many fibre plans — port
  forwarding is impossible for you no matter what you do. The tunnel does not
  care, because it never needed an inbound address in the first place.
- **A dynamic IP address does not matter.** The tunnel re-registers itself. You
  never need dynamic DNS.

The GoldenCloud server itself binds to `127.0.0.1`, which means the operating
system will not accept a connection to it from anywhere except the machine
itself. Even a device plugged into the same office switch cannot reach it. The
tunnel is the only door, and it opens outwards.

## Where the credentials live, and their permissions

Two secret files are created during setup. Neither belongs in git — both are
already covered by [`../../.gitignore`](../../.gitignore).

| File | Created by | Lives at | Permissions | What it is |
| --- | --- | --- | --- | --- |
| `cert.pem` | `cloudflared tunnel login` | `~/.cloudflared/cert.pem` | `0600`, owned by the admin user | Your **account** certificate. Authorises creating, deleting, and DNS-routing tunnels. Needed only when you run tunnel admin commands. |
| `<UUID>.json` | `cloudflared tunnel create` | `/etc/cloudflared/<UUID>.json` | `0600`, owned by `root` | The **tunnel** credentials. This is what the running service uses. Anyone holding it can impersonate your tunnel. |

Check them:

```bash
sudo ls -l /etc/cloudflared/
ls -l ~/.cloudflared/
```

Both should show `-rw-------`. If either shows anything wider, fix it:

```bash
sudo chmod 0600 /etc/cloudflared/*.json
chmod 0600 ~/.cloudflared/cert.pem
```

If a credentials file is ever exposed, delete and recreate the tunnel:

```bash
sudo systemctl stop cloudflared
cloudflared tunnel delete goldencloud
cloudflared tunnel create goldencloud
```

Then copy the new JSON into `/etc/cloudflared/`, update the UUID in
`config.yml`, re-run `cloudflared tunnel route dns`, and start the service
again.

## How to check tunnel health

**Is the service running?**

```bash
sudo systemctl status cloudflared
```

Expect `Active: active (running)`. Anything else means the tunnel is down and
staff will see a Cloudflare error page.

**Are the connections up?**

```bash
sudo journalctl -u cloudflared -n 40 --no-pager
```

Expect four lines like
`Registered tunnel connection connIndex=0 ... location=lhr01`, one per
connection index 0–3, to four different Cloudflare locations. Fewer than four
is survivable; zero means no traffic is reaching you.

**What does Cloudflare think?**

```bash
cloudflared tunnel list
cloudflared tunnel info goldencloud
```

`list` shows a `CONNECTIONS` column — it should not be empty. `info` names the
edge locations currently serving the tunnel.

**Is the origin actually answering?**

```bash
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/
```

Expect `401`. That means the GoldenCloud server is alive and correctly
demanding a username and password. `000` or `Connection refused` means the
server is down, and the tunnel will be returning 502 to everybody — fix the
server, not the tunnel.

**End to end, from the outside:**

```bash
curl -sS -o /dev/null -w '%{http_code}\n' https://cloud.example.com/
```

Expect `401` again. `530` or `1033` means the tunnel is not connected. `502`
means the tunnel is connected but the server behind it is not answering.

## Files here

- [`config.yml.template`](config.yml.template) — the tunnel configuration to
  copy to `/etc/cloudflared/config.yml` and fill in.
- This README.
