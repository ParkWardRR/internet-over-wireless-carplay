#!/bin/sh
# Build the emulated "box" firmware: OpenWrt snapshot (what the EWS377 fork
# tracks) for x86-64 so it runs under KVM, plus mac80211_hwsim virtual radios
# and the tools the scenarios need. Runs on build-host:
#
#   emu/run.sh build          (wraps this in the carplay-emu container)
#
# Caches the Image Builder in ~/builds/.cache/openwrt (REFRESH=1 re-downloads).
# Output: emu/out/openwrt.img (+ emu/out/VERSION with the snapshot build id).
set -eu

TARGET=${TARGET:-x86/64}
IB=openwrt-imagebuilder-$(echo "$TARGET" | tr / -).Linux-x86_64
URL=https://downloads.openwrt.org/snapshots/targets/$TARGET/$IB.tar.zst
CACHE=/cache/openwrt
OUT=/work/emu/out
KEY=/cache/carplay-emu/id_ed25519

PACKAGES="kmod-mac80211-hwsim wpad-openssl hostapd-utils wpa-cli iw-full ip-full tcpdump conntrack kmod-veth uhttpd odhcp6c odhcpd-ipv6only dnsmasq -ppp -ppp-mod-pppoe"

mkdir -p "$CACHE" "$OUT" "$(dirname "$KEY")"
if [ ! -d "$CACHE/$IB" ] || [ "${REFRESH:-0}" = 1 ]; then
	echo "downloading $URL"
	rm -rf "${CACHE:?}/$IB"
	wget -q -O "$CACHE/$IB.tar.zst" "$URL"
	tar -C "$CACHE" --zstd -xf "$CACHE/$IB.tar.zst"
	rm -f "$CACHE/$IB.tar.zst"
fi

# Test-only SSH key for the harness (kept in the cache, never in git).
[ -f "$KEY" ] || ssh-keygen -q -t ed25519 -N '' -C carplay-emu -f "$KEY"

FILES=$OUT/files
rm -rf "$FILES"
cp -a /work/emu/files "$FILES"
mkdir -p "$FILES/etc/dropbear"
cp "$KEY.pub" "$FILES/etc/dropbear/authorized_keys"
chmod 600 "$FILES/etc/dropbear/authorized_keys"
mkdir -p "$FILES/root/carplay"
cp /work/scripts/openwrt-carplay.sh "$FILES/root/carplay/"
mkdir -p "$FILES/usr/bin"
install -m 755 "$OUT/emuagent" "$FILES/usr/bin/emuagent"

cd "$CACHE/$IB"
make -s image PROFILE=generic PACKAGES="$PACKAGES" FILES="$FILES" \
	BIN_DIR="$OUT/bin" ROOTFS_PARTSIZE=256 >"$OUT/build.log" 2>&1 \
	|| { tail -30 "$OUT/build.log"; exit 1; }

img=$(find "$OUT/bin" -name '*-ext4-combined.img.gz' | head -1)
gunzip -c "$img" > "$OUT/openwrt.img" 2>/dev/null || true   # gzip warns about trailing padding
manifest=$(find "$OUT/bin" -name '*.manifest' | head -1)
{
	echo "imagebuilder: $IB (downloaded $(date -r "$CACHE/$IB" +%F))"
	grep -E '^(kernel|wpad-openssl|odhcpd-ipv6only|netifd|firewall4|kmod-mac80211-hwsim) ' "$manifest"
} > "$OUT/VERSION"
echo "built $OUT/openwrt.img"; cat "$OUT/VERSION"
