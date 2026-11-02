# Test log

One row per test. Put the PSK, VIN, SSID digits and MACs in `secrets/`, never here.

| Date | Phase | Setup | Result | Notes |
|---|---|---|---|---|
| 2026-11-01 | P0 | iPhone → Settings → Wi-Fi → car network | CarPlay page: no IP/router/DNS fields, no password field. **Weak Security** label. Has **Check For Internet Access** toggle + Manage CarPlay | Next: Passwords app → Wi-Fi, and Settings → Wi-Fi → Edit → Known Networks |
| 2026-11-01 | — | Read the Bimmerfest thread | Car net 172.16.224.0/24, no gateway; 2nd client OK; LTE router at .254 + manual iPhone IP worked (2022); iOS shows the PSK since ~2025 | Script now also offers 172.16.224.254 + NAT44 |

## P0 — iPhone, car network

| Field | Value |
|---|---|
| SSID pattern | `BMW<5 digits> CarPlay` |
| Security | "Weak Security" (WPA/TKIP or WPA/WPA2 mixed, to confirm in P1) |
| IP settings | not shown for CarPlay networks |
| Password revealable? | no |
| Check For Internet Access | present; **turn it on** for P2 |
| Head unit | factory NBT EVO |
| iOS version | |

Offline maps: download the area in Google Maps (and Apple Maps), go to the garage, Airplane mode + Wi-Fi to the car only, try search + route.

## P1 — car AP from the Mac

Car on, Mac near the car:

```sh
sudo wdutil info                     # current link
system_profiler SPAirPortDataType | sed -n '/Other Local Wi-Fi Networks/,$p' | grep -A8 -i carplay
```

Record band, channel (DFS?), PHY mode, security (WPA/WPA2, TKIP/CCMP), BSSID OUI.

## P2 — MacBook proof of concept (IPv6 RA)

1. `cp secrets.example.env secrets/carplay.env` and fill in the SSID + PSK.
2. Plug the USB-C Ethernet adapter into the garage LAN (needs a 172.16.x address and global IPv6).
3. `sudo scripts/poc-mac-nat.sh up`. It joins the car Wi-Fi, adds `fd5c:ba1e:f32:1::1/64`, starts `rtadvd` (60 s router lifetime, RDNSS 2001:4860:4860::8888 + 2606:4700:4700::1111) and NAT66 out of the Ethernet.
4. iPhone: Settings → Wi-Fi → car network → **Check For Internet Access ON**. Start CarPlay.
5. `sudo scripts/poc-mac-nat.sh status`. Success looks like the iPhone listed with an `fd5c:ba1e:f32:1:` address and NAT66 states > 0.
6. Google Maps on CarPlay: search + route. Record: does it work, latency, CarPlay stable?
7. `sudo scripts/poc-mac-nat.sh down`.

| Check | Result |
|---|---|
| Mac joined car SSID (2nd client allowed) | |
| Mac got car DHCP IPv4 | |
| iPhone took the SLAAC address (no client isolation, RA accepted) | |
| NAT66 states from the iPhone | |
| Maps search + route over CarPlay, Check For Internet Access **on** | |
| Same with it **off** | |
| Head unit took an `fd5c:` address? (`status` flags it) | |
| Apple Maps search (some hosts IPv4-only) | |
| CarPlay stayed connected | |

If the iPhone takes the address but still says no internet: try the DHCPv4-race fallback (README).

### DHCP timing (decides whether the IPv4 race is winnable)

The car owns IPv4: its head unit is the DHCP server and sends no router option, and iOS hides manual IP settings for CarPlay networks. The only IPv4 way in is answering the phone's DISCOVER faster than the car. Measure the car's reply time while the Mac is on the car Wi-Fi:

```sh
sudo tcpdump -ni en0 -tttt 'udp port 67 or udp port 68'
# then on the iPhone: Wi-Fi off/on (or disconnect/reconnect CarPlay)
```

| Measure | Value |
|---|---|
| DISCOVER → car OFFER (ms) | |
| REQUEST → car ACK (ms) | |
| Lease time, server identifier (`172.16.224.1`?) | |
| Does the phone use INIT-REBOOT (REQUEST without DISCOVER) on rejoin? | |

Under a few ms from the car means a box rarely wins; tens of ms or more makes a race plausible.

## P4 — exit test

Drive out with the box running. Time from leaving the garage to Maps updating over cellular (router lifetime is 60 s).
