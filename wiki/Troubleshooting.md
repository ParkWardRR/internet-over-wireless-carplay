# Troubleshooting

Symptom first. Each entry says how to tell, and what it actually is.

## Maps does nothing, but the box looks perfect

**By far the most common.** The box reports a healthy link, packets are flowing, and your apps behave as if there is no connection.

**Cause:** iOS gives a CarPlay network the lowest service rank. Background daemons use it; applications do not. On one instrumented drive 1.36 MB moved through the box and no user-facing app touched a byte.

**Fix:** on the phone, Settings → Wi-Fi → the car's network → **Check For Internet Access** → turn on **Use For Internet**.

**Confirm it:**

```sh
sudo nft list counters table inet carplayd
```

`probe_<car>` is the traffic iOS sends to validate the network. Zero means the tap has not landed. The daemon also logs `phone_unvalidated` with the fix in the message, and `validated` once real traffic appears.

**It comes back after a car restart.** After the first tap iOS mostly re-validates by itself across short absences (4–24 seconds, measured), but a real restart needs the tap again. Treat the tap as required and the automatic re-check as a bonus.

## CarPlay audio stutters when I use the internet hard

**Cause:** everything is relayed by the head unit, so your download crosses the air twice and competes with CarPlay's own stream — and the head unit's relay collapses well before the Wi-Fi does.

**Fix:** cap the box's traffic toward the car. The default is 2 Mbit/s. If it still stutters, lower it rather than raise it.

**Do not** raise the cap because the channel looks idle. It does look idle; it is not the constraint. See [[Bandwidth and Airtime]].

## CarPlay disconnected completely and will not re-pair

**Cause:** sustained saturation. Measured: the head unit dropped CarPlay and needed a **hard reset** to pair again.

**Fix:** reset the head unit, then make sure the cap is on before loading the link again. Never run a saturation test on a live car with the cap off.

## Websites that work on cellular fail on the car link

**Cause:** our path is IPv6-only, and some sites have no IPv6 at all. Reddit's main site is one. Those need DNS64 + NAT64 — a translator — and if anything blocks the translated addresses, DNS answers perfectly and nothing connects.

**Two real failure modes, both hit here:**

1. **The firewall rejected the translated addresses.** The synthesised prefix sits inside the private IPv6 range that the isolation rules reject, so every IPv4-only *public* site was refused while only a few named hosts were allowed. Symptom: DNS resolves, connections hang.
2. **IPv4 forwarding was switched off.** The translator did its job perfectly and the kernel dropped every translated packet. Symptom: identical to the above, and the box itself can reach the site fine.

**Check:**

```sh
sysctl net.ipv4.ip_forward          # must be 1 for NAT64 to work at all
sudo nft list counters table inet carplayd | grep -A1 nat64
```

A nonzero NAT64 counter means a phone really reached public IPv4 through the translator.

## The box keeps joining and leaving the same car

**Cause, if the signal is strong:** a learned threshold that became too eager. If a car parks much closer than usual, a threshold derived relative to that strong signal can fire on perfectly ordinary variation.

This is fixed — a learned threshold may now be *more patient* than configured, never more eager — but if you see it, check the events log for `leave` lines at a healthy signal.

**Workaround:** set `learn = off` to fall back to the configured numbers, and restart.

## The capture recorded nothing

**Cause:** the USB adapter stops receiving in monitor mode while the interface stays up. The process keeps running happily with nothing to read.

The tools now detect this: a run of silence on a channel that always has neighbours is treated as a stall, logged once, and the interface is rebuilt. If you are using something else, add that check — "no frames" and "quiet channel" are not the same thing.

## Reading the daemon's log

```sh
sudo tail -f /var/lib/carplayd/events.log
```

| Event | Meaning |
|---|---|
| `seen` / `select` / `connected` | found a car, chose it, associated |
| `serving` | route advertised; the link is up |
| `shaped` | the traffic cap was applied |
| `validated` / `phone_unvalidated` | whether your apps can use it |
| `leave` / `goodbye` | leaving deliberately, with the route withdrawn |
| `link_lost … reason=3 … locally_generated=0` | **the head unit ended it** — usually the car being switched off |
| `hunt_backoff` | an idle radio is backing off so it stops disturbing a live link |
| `device_missing` / `device_gone` | the capture adapter vanished |

A `reason=3` loss at a strong signal is not a fault. That is what a head unit sends when it shuts down.
