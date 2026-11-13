# Building kernel modules the garage box's vendor kernel left out

The Orange Pi 4A (Allwinner T527, `allwinner,a523` / `sun55iw3p1`) runs a vendor
kernel, `5.15.147-sun55iw3`, that ships **322 modules** and compiles out a lot
of things this project needs:

```
# CONFIG_TUN is not set          -> no /dev/net/tun, so no userspace NAT64
# CONFIG_ATH9K is not set
# CONFIG_ATH9K_HTC is not set    -> USB AR9271 dongles bind to nothing
```

There are no kernel headers (`/lib/modules/$(uname -r)/build` is absent), no
`linux-headers-*` package, and the box is not Armbian — its apt sources are a
Debian mirror, LLVM and Caddy. So DKMS is out, and so is anything that needs to
compile against the running kernel on the box itself.

Out-of-tree modules still work, and three facts are why:

- `# CONFIG_MODULE_SIG is not set` — unsigned modules load.
- `# CONFIG_MODVERSIONS is not set` — no per-symbol CRCs. Only the **vermagic**
  string has to match: `5.15.147-sun55iw3 SMP preempt mod_unload aarch64`.
- `CONFIG_MAC80211=y` and `CONFIG_CFG80211=y` are already built in, so a
  wireless driver module has a stack to bind to.

The second point cuts both ways. Nothing checks symbol compatibility at load
time, so a module built from a *slightly* different configuration will load and
then misbehave. The safety of this whole procedure rests on step 4.

## Recipe

Builds run on the build host (6 cores); roughly 25 minutes.

1. **Take the config off the box.** `/proc/config.gz` is the only authoritative
   record of how the running kernel was built.

   ```sh
   ssh <box> 'zcat /proc/config.gz' > running.config
   ```

2. **Clone the matching source.** Orange Pi publishes it; the branch
   `orange-pi-5.15-sun55iw3` of `github.com/orangepi-xunlong/linux-orangepi`
   has `VERSION/PATCHLEVEL/SUBLEVEL = 5.15.147`, exactly the running kernel.

3. **Force the local version.** The box has `CONFIG_LOCALVERSION=""`, so the
   `-sun55iw3` suffix comes from the tree, and without it vermagic will not
   match:

   ```sh
   printf -- "-sun55iw3\n" > localversion
   ```

4. **Enable what you need, then diff the result.** This is the step that makes
   the rest safe:

   ```sh
   cp ../running.config .config
   ./scripts/config --file .config --module CONFIG_ATH9K_HTC --module CONFIG_TUN
   make ARCH=arm64 CROSS_COMPILE=aarch64-linux-gnu- olddefconfig
   diff <(grep -vE '^#|^$' ../running.config | sort) \
        <(grep -vE '^#|^$' .config | sort)
   ```

   Every difference must be one you intended. Expect noise from detected
   toolchain values (`CC_VERSION_TEXT`, `GCC_VERSION`, `AS_VERSION`,
   `LD_VERSION`, `PAHOLE_VERSION`, `CC_CAN_LINK`) — those do not affect struct
   layout. `CONFIG_GCC_PLUGINS=y` disappearing is also fine *provided* no actual
   plugin was enabled; check that `GCC_PLUGIN_RANDSTRUCT` and the
   `GCC_PLUGIN_STRUCTLEAK_*` options are all unset in the running config, since
   those do change layouts.

5. **Cross-compile in a container**, with a GCC close to the original (the box's
   kernel was built with Arm's 11.2.1; Ubuntu 22.04 ships 11.4):

   ```sh
   podman run --rm -v "$PWD":/src:Z -w /src <image> \
     make ARCH=arm64 CROSS_COMPILE=aarch64-linux-gnu- -j6 Image modules
   ```

   Building `Image` too is not strictly required, but it gives a complete
   `Module.symvers` so modpost's output is meaningful instead of a wall of
   undefined-symbol warnings.

6. **Install only the new modules**, into `updates/`, never over a vendor one:

   ```sh
   ssh <box> 'sudo tar x -C /lib/modules/$(uname -r)/updates'
   ssh <box> 'sudo sh -c "ulimit -n 65536; depmod -a"'
   ```

7. **Confirm vermagic before loading:** `modinfo <module>.ko` must print
   `5.15.147-sun55iw3 SMP preempt mod_unload aarch64`, character for character.

Make loading persistent with `/etc/modules-load.d/`.

## Traps

**`ATH9K_HTC` silently turns on a mac80211 option.** Its Kconfig has
`select MAC80211_LEDS if LEDS_CLASS=y`, and the box has `LEDS_CLASS=y`, so
`olddefconfig` sets `CONFIG_MAC80211_LEDS=y` — an option the *running*,
built-in mac80211 was compiled **without**. Delete that `select` from
`drivers/net/wireless/ath/ath9k/Kconfig`; all the ath9k LED code is behind
`#ifdef CONFIG_MAC80211_LEDS` and simply compiles out. This is the one
difference the step-4 diff is really there to catch.

**Do not `tar` the modules from macOS.** AppleDouble `._*` files ride along into
`updates/` and `depmod` chokes on them:

```
depmod: ERROR: failed to load symbols from .../._tun.ko: Exec format error
```

Set `COPYFILE_DISABLE=1`, or delete `._*` afterwards.

**`depmod` needs a raised fd limit here** — `ulimit -n 65536`. Below that it
fails across the vendor module tree with `Too many open files`, and it takes
several minutes either way.

## Built so far

`tun.ko` (unblocks tayga/NAT64) and `ath.ko`, `ath9k_hw.ko`, `ath9k_common.ko`,
`ath9k_htc.ko` for the USB radios in [usb-radios.md](usb-radios.md). Both load
at boot and survive a reboot.
