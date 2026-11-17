# Hardware

## The box

Any small Linux machine with wired Ethernet. Known working: an Orange Pi 4A (Allwinner T527) for the daemon, and a Raspberry Pi for the in-car recorder.

What matters more than the board is the **kernel**, because of the trap below.

## Wi-Fi adapters

You need a 2.4 GHz adapter that can run as a **client**, and for measurement work also as a **monitor**.

Known working: the **AR9271** (`ath9k_htc`). Cheap, well supported, and does both modes.

**One adapter per car.** They cannot be shared between two cars — a single radio can only be associated to one network at a time, so two cars on one radio means taking turns.

### Trap 1: your kernel may not have the driver

The Orange Pi's vendor kernel shipped **neither `ath9k_htc` nor TUN**. The adapters bound to nothing, and the NAT64 translator could not run at all. Both had to be cross-built out of tree against the published vendor source.

What made that survivable: module signing and version checks were both off, so only the kernel's `vermagic` string had to match. What nearly broke it: enabling the driver quietly selected another option that the running kernel had been built *without*. The only thing that caught it was diffing the generated configuration against the running kernel's own until nothing but the intended options differed.

**A Raspberry Pi kernel has `ath9k_htc` as a standard module**, so none of this applies there. If you have the choice, that is the easier path.

### Trap 2: adapters are not named stably

USB probe order moved the onboard radio between device numbers between boots, which silently reassigned which adapter served which car. **Name them by MAC address** with a udev rule, not by probe order.

### Trap 3: NetworkManager will fight you

A newly plugged adapter is managed by default, and NetworkManager will reset the interface type out from under a monitor-mode capture. Mark it unmanaged, and make that survive a redeploy rather than doing it by hand.

### Trap 4: the adapter lies about its own statistics

`ath9k_htc` reports **retry and airtime counters as permanently zero** — they are unimplemented, not good news. Judging link quality from them is how you conclude "the air is clean" about a link retransmitting one frame in five.

Judge it by the negotiated rate instead, or measure retries from the air with a monitor interface. `survey dump` also returns nothing on this driver, so channel-busy time is not available from it either.

## Placement

The signal environment matters more than you would expect. In the garage this was built for, the box sits behind a cinderblock wall a few feet from the cars, and:

- a parked car reads far weaker than free space would suggest
- a car parked *further away* sometimes reads *stronger*
- **the signal of a parked car overlaps the signal at which the link dies**

That last point is why fixed signal thresholds cannot work here, and why the daemon learns them instead. See [[Design Decisions]].

## For measurement: put the recorder in the car

Not next to the box. From the garage, the phone registered −76 to −84 dBm and its traffic was under-counted roughly tenfold. Inside the car it was −36 dBm. Any measurement of what CarPlay itself uses has to be made from inside the car. See [[Measuring]].

## Power

The in-car recorder runs on a battery pack. That shapes the software: logs are flushed to disk every second, so an abrupt power cut loses one second rather than the file, and output is capped so it cannot fill the card unattended.
