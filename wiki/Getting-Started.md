# Getting Started

## What you need

- A small Linux box — Raspberry Pi, Orange Pi, or similar. It stays near the car.
- **Wired Ethernet** from that box to your internet.
- A **2.4 GHz USB Wi-Fi adapter that can run as a client**. One per car — they cannot be shared. See [[Hardware]] for which ones and the traps.
- An iPhone on **iOS 27 or newer**. The *Use For Internet* switch does not exist before that.
- The car's CarPlay Wi-Fi password.

## 1. Get the CarPlay Wi-Fi password

No jailbreak needed. On iOS 27:

> Settings → Wi-Fi → **Edit** → Known Networks → the car's network → **Password**

Face ID, then Copy.

The network is named `BMW##### CarPlay`, where the digits are **the last five of your VIN**. Treat both the password and the name as secrets — do not paste them into issues, logs or screenshots.

## 2. Store the secrets

Copy `secrets.example.env` to `secrets/carplay.env`:

```sh
CAR_A_SSID='BMW##### CarPlay'
CAR_A_PSK='the-password'
```

`secrets/` is gitignored, and a pre-commit hook blocks the known values from being committed. That hook has caught a real leak twice, so leave it installed:

```sh
ln -sf ../../scripts/check-secrets.sh .git/hooks/pre-commit
```

## 3. Try it once, by hand

Before installing anything permanent:

```sh
sudo scripts/poc-linux.sh up      # join the car, advertise a route, NAT out
sudo scripts/poc-linux.sh status  # is the phone there? are there NAT flows?
sudo scripts/poc-linux.sh down    # undo everything
```

`up` refuses to start unless it really got an address on the car's network, because a failed join would otherwise advertise a route on your home Wi-Fi.

## 4. Install the daemon

For anything beyond a one-off, install **carplayd**. It handles multiple cars, learns its own signal thresholds, leaves cleanly when you drive away, and caps its own traffic.

```sh
SBC=user@your-box BUILD=user@your-build-host scripts/deploy-carplayd-sbc.sh
```

Both variables are required. The script's defaults are deliberately scrubbed placeholders and it refuses to run on them — left alone they do not fail, they hang for minutes inside `rsync` while looking like a slow build.

Roll back with `scripts/deploy-carplayd-sbc.sh rollback`.

## 5. Start CarPlay, then tap the switch

Start the car and let CarPlay connect as usual. Then, on the phone:

> Settings → Wi-Fi → the car's network → **Check For Internet Access** → turn on **Use For Internet**

**This step is required and it is the one everybody misses.** Until you do it, your apps will ignore the connection completely while the box reports perfect health. See [[Troubleshooting]].

## 6. Confirm it worked

Open Maps on CarPlay and search for something you have never searched before, so it cannot come from cache. Search and routing should work with no cell signal.

From the box:

```sh
sudo nft list counters table inet carplayd
```

`probe_<car>` counts the packets iOS sent to validate the network, and `fwd_<car>` counts what it forwarded. A nonzero `probe_` means the tap landed. The daemon also logs it directly:

- `phone_unvalidated` — a phone is present but your apps are not using the link, with the fix in the message
- `validated` — real traffic is flowing

## What "working" looks like

| | |
|---|---|
| Join time, car appears → serving | well under a second |
| Round trip to the phone | tens of milliseconds, with occasional spikes from Wi-Fi power saving |
| Bulk download | capped on purpose, around 250 kB/s at the default limit |
| Maps | searches, routes and loads tiles with no cellular |

If bulk downloads feel slow, that is the cap doing its job. Read [[Bandwidth and Airtime]] before raising it.
