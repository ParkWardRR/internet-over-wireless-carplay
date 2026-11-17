# The CarPlay Network

What the car's wireless-CarPlay Wi-Fi actually is. Everything here is **measured** on two factory NBT EVO head units unless marked otherwise.

## Two kinds of car

Work this out first — it decides whether this project applies at all.

| Pairing style | What CarPlay runs over | Internet? |
|---|---|---|
| **Wi-Fi pairing** | the car's own hotspot | yes, you already have it |
| **Bluetooth pairing** | a private CarPlay-only network | **none** |

The NBT EVO is the second kind, which is why this project exists. *(source: Audi technical service bulletins describing both designs.)*

A quick tell: if the CarPlay network page on iOS shows **"Weak Security"** and hides IP settings, you are probably in the second group.

## The radio link

| Property | Value |
|---|---|
| Band / channel | 2.4 GHz, channel 11 |
| Width | 20 MHz (HT20) |
| Spatial streams | **one** — so ~65 Mbit/s PHY peak, 25–30 Mbit/s real |
| Beacon offers | WPA + WPA2 mixed, **TKIP group cipher**, TKIP/CCMP pairwise |
| Actually negotiated | WPA2-PSK, CCMP pairwise, TKIP group, Wi-Fi 4 |
| Access point | the head unit itself |

The TKIP group cipher is why iOS labels it **"Weak Security"**. A box joining it needs WPA2 with a TKIP group cipher permitted; WPA1 is never used.

Apple's own accessory guidance recommends **2×2 MIMO** for head units. These are 1×1, which is a large part of why capacity is tight. *(source.)*

## Addressing

| | |
|---|---|
| IPv4 | a private /24, handed out by the head unit |
| IPv4 router option | **none** |
| DNS servers | **none** |
| IPv6 router advertisements from the car | **none** |
| Head unit | the access point, at the first address in its range |

So: an address and nowhere to go. That is the whole reason this project is possible *and* necessary — there is no IPv6 router on the link, so adding one conflicts with nothing.

## The two properties that make it work

**A second client is allowed.** The head unit does not limit itself to the phone. The box can sit alongside it.

**There is no client isolation.** The two clients can exchange traffic. Without this, nothing here would be possible.

## The two properties that make it hard

**Everything between clients is relayed by the head unit** — there is no direct link negotiated between phone and box. Every byte crosses the air twice and competes with CarPlay's own stream.

**The head unit is not a router.** Its relay collapses far below the link's capacity. See [[Bandwidth and Airtime]].

## The beacon

The car advertises, in its 802.11u Interworking element:

- **Internet bit: off**
- Access network type: private
- Venue group: vehicular
- **No Apple Device information element at all**

And yet **iOS still offers to check for internet access, and the check passes** over a purely IPv6 route. So that bit is *not* what gates the prompt — a useful negative result, since it rules out the obvious theory that advertising "internet available" would remove the tap.

## The password

- The network name's digits are **the last five of your VIN**.
- The key is 10 random alphanumerics, delivered to the phone over Bluetooth during pairing.
- **iOS 27 will show it to you** with no jailbreak: Settings → Wi-Fi → Edit → Known Networks → the car → Password.
- **Re-pairing a phone regenerates it.** That is also how you would rotate it if it leaked.

Sniffing it is not realistic (10 random characters) and the Bluetooth exchange is encrypted.

## Prior art

The only other public implementation on this head unit that we could find: in February 2022 someone put an LTE router on the CarPlay Wi-Fi at a static address and set the iPhone's IP settings manually, and got internet with CarPlay running. That confirms the second-client property years earlier.

We found no public description of the IPv6 route, nor of the iOS 27 *Use For Internet* behaviour, across roughly 30 searches in four languages plus code search. **Not finding something is not proof nobody has done it.**
