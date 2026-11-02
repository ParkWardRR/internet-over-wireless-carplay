# Test log

One row per test. Put the PSK, VIN and MACs in `secrets/`, never here.

| Date | Phase | Setup | Result | Notes |
|---|---|---|---|---|
| | P0 | iPhone car-network details | | |

## P0 — iPhone, car network details

Settings → Wi-Fi → (i) next to the car SSID.

| Field | Value |
|---|---|
| SSID pattern | |
| IP address | |
| Subnet mask | |
| Router | (expected empty) |
| DNS | |
| Password revealable? | |
| iOS version | |
| Head unit (NBT EVO ID5/ID6 / retrofit) | |

Offline maps: download the area in Google Maps (and Apple Maps), go to the garage, Airplane mode + Wi-Fi to the car only, try search + route.

## P1 — car AP from the Mac

Car on, Mac near the car:

```sh
sudo wdutil info                     # current link
system_profiler SPAirPortDataType | sed -n '/Other Local Wi-Fi Networks/,$p' | grep -A8 -i carplay
```

Record band, channel (DFS?), PHY mode, security (WPA2/WPA3), BSSID OUI.

## P2 — MacBook proof of concept

1. Mac: join the car SSID with the PSK; USB-C Ethernet to the garage LAN.
2. `sudo sysctl -w net.inet.ip.forwarding=1`
3. pf NAT (`en7` = Ethernet uplink, `CAR_NET` = car subnet from P0, e.g. `192.168.1.0/24`). This replaces the main pf ruleset until undone:
   ```sh
   CAR_NET=192.168.1.0/24
   echo "nat on en7 from $CAR_NET to any -> (en7)" | sudo pfctl -ef -
   ```
4. iPhone, car SSID: Configure IP → Manual, address = a free IP in the car subnet outside its DHCP range, router = the Mac's car-Wi-Fi IP, DNS = 172.16.1.69.
5. Start CarPlay, open Google Maps, search + route. Note: CarPlay stable? Latency?
6. Undo: `sudo pfctl -f /etc/pf.conf; sudo pfctl -d; sudo sysctl -w net.inet.ip.forwarding=0`; set the iPhone back to Automatic.

## P4 — exit test

Drive out with the manual config in place. Time from leaving the garage to Maps updating over cellular.
