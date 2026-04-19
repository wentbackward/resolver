---
id: hostname-policy.md
source_note: internal naming convention cheat sheet
tokens_estimate: 400
suitable_for: [sweep-B, multi-turn]
---
# Hostname policy

Machine hostnames follow a per-class convention:

- GPU boxes use a service-prefixed name: `spark-01`, `spark-02`, etc. The number is allocated in the order of physical installation, not model class.
- Edge VMs / VPSes use a single word that is either a fictional character or an aphorism. Examples in use: `claw`, `marvin`.
- Dev machines (laptops, workstations) use character names too, but the character is chosen by the developer. `marvin` happens to be both a VPS and a laptop in current usage — the cluster resolver disambiguates by role.
- Small IoT / homelab devices use food names: `fragola` (strawberry), `limone`, `pomodoro`.

DNS entries shadow the hostnames in the `.internal` zone and the `.lab` zone. External TLS cert renewal goes through Caddy on `claw`; there is no separate CA.

## Building map

Racks are numbered per the lab floor plan; the GPU rack in building 7 is the legacy location from before the lab move (we still track decom work there).
