# One radio per car: two USB AR9271 dongles on the garage box

The garage box now serves both cars at once, each on its own radio, instead of
time-sharing the onboard Broadcom. This retires the dual-STA question (B3 in
[two-cars.md](two-cars.md)): rather than ask one brcmfmac radio to hold two
associations, each car gets a dedicated one.

The hardware is the part [fast-connect-plan.md](fast-connect-plan.md) already
specified for the monitor role — a USB **Atheros AR9271** (`0cf3:9271`,
`ath9k_htc`, 2.4 GHz, ~$15). Two of them, one per car.

| wiphy | MAC | USB port | carplayd STA | car |
|---|---|---|---|---|
| `car-a` | `aa:bb:cc:00:00:01` | 1-1.4 | `cara` | A (F32) |
| `car-b` | `aa:bb:cc:00:00:02` | 3-1 | `carb` | B (F22) |

Both report channel 11 at 20 dBm, 2.4 GHz only, and
`#{ managed, P2P-client } <= 2` — enough for carplayd's STA.

## carplayd needed no changes

`buildLinks` in `main.go` already makes **one daemon per radio** out of
`cfg.radios()`, each with its own wpa_supplicant control directory, the first
link owning the box-wide resources (the nftables table that has to name every
car link, NAT, shared learned state). Serving one car at a time is a property
of a single daemon, not of the box. So the whole change is config:

```ini
[car A]
phy = car-a
sta = cara
...
[car B]
phy = car-b
sta = carb
```

`carplayd -check` then prints two links, and `carplayd -nft` shows the firewall
naming `cara` and `carb` with separate `fwd_`/`probe_` counters per car.

## The driver had to be built

The Orange Pi 4A vendor kernel (`5.15.147-sun55iw3`, Allwinner T527) compiles
out the whole `ath` family **and** TUN, ships no kernel headers, and has no
Armbian repo to get them from. The modules were cross-built out-of-tree instead;
the full recipe and its safety argument are in
[kernel-modules.md](kernel-modules.md). The same build produced `tun.ko`, which
is what a userspace NAT64 (tayga) needs — see [ipv4-options.md](ipv4-options.md).

Firmware comes from Debian's **`firmware-ath9k-htc`** (`htc_9271-1.4.0.fw`).
`firmware-atheros` does *not* contain it.

## Two traps worth knowing

**phy numbering is not stable.** The onboard Broadcom was `phy0` before the
dongles and `phy1` after, because USB probe order decides the numbering. So
`carplay-radios.service` (`/usr/local/sbin/carplay-name-radios`) renames the
wiphys **by MAC** to `car-a`/`car-b` before carplayd starts, and waits up to
30 s for both to enumerate — USB probing is asynchronous, and without the wait
boot races carplayd, which then dies on a missing phy.

**The driver's default vif collides with carplayd's.** `ath9k_htc` creates a
`wlx<mac>` netdev at probe time carrying the same MAC as the STA carplayd adds,
and mac80211 refuses a second interface with a duplicate MAC on one phy. The
error names the wrong thing entirely:

```
ip link set cara up: RTNETLINK answers: Name not unique on network
```

The same script deletes that default vif. NetworkManager also has to be told to
leave `cara`, `carb` *and* both `wlx*` names alone in
`/etc/NetworkManager/conf.d/90-carplayd.conf`, or it claims the dongles.

## Verified

Survives a reboot unattended: modules load from `/etc/modules-load.d/`, the
naming unit renames both radios, carplayd starts both links and each scans
independently (two scan results ~20 ms apart, separate `cara`/`carb` BSS
events). Association itself is still untested — both cars were away.
