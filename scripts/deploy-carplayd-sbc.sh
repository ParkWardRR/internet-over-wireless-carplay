#!/bin/sh
# Deploy carplayd to garage-sbc (Debian 12, brcmfmac, NetworkManager), from the
# Mac. Builds on build-host, copies the arm64 binary over, writes /etc/carplayd.conf
# from secrets/carplay.env, takes wlan0 away from NetworkManager, removes the
# interim carplay-box.sh setup and starts carplayd.service.
#
#   scripts/deploy-carplayd-sbc.sh            deploy / update
#   scripts/deploy-carplayd-sbc.sh rollback   back to carplay-box.sh (NetworkManager)
#
# The configured thresholds are only a cold-start fallback: with learn = on the
# box measures each car's own healthy band and derives join/leave/rejoin from
# it, which is the only workable answer behind a cinderblock wall where parked
# and dying signal overlap (docs/findings.md).
#
# Keep this heredoc as the single source of truth for the box's config. It was
# edited by hand once, which left the running box with settings this script did
# not know how to write -- so the next run of the script would have silently
# reverted them.
set -eu
HERE=$(cd "$(dirname "$0")/.." && pwd)
SBC=${SBC:-user@garage-sbc.local}
BUILD=${BUILD:-user@build-host.local}
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if [ "${1:-}" = rollback ]; then
	ssh "$SBC" 'sudo systemctl disable --now carplayd 2>/dev/null; sudo /usr/sbin/nft delete table inet carplayd 2>/dev/null; sudo rm -f /etc/NetworkManager/conf.d/90-carplayd.conf; sudo nmcli general reload; sudo nmcli dev set wlan0 managed yes; cd ~/carplay-poc && sudo ./carplay-box.sh install'
	exit 0
fi

. "$HERE/secrets/carplay.env"
[ -n "${CAR_B_SSID:-}" ] && [ -n "${CAR_B_PSK:-}" ] && [ -n "${CAR_A_SSID:-}" ] && [ -n "${CAR_A_PSK:-}" ] \
	|| { echo "secrets/carplay.env needs CAR_A_* and CAR_B_*" >&2; exit 1; }

echo "building on build-host..."
rsync -a --delete --exclude secrets/ --exclude .git/ --exclude emu/out/ --exclude 'ios/CarNetProbe/*.xcodeproj' \
	"$HERE/" "$BUILD:builds/carplay-deploy/"
# scripts/release.sh, not a bespoke build line: whatever lands on the box has to
# be reproducible from the tree, and it gofmt/vet/race-tests before it builds.
ssh "$BUILD" 'cd ~/builds/carplay-deploy && rm -f out/carplayd-linux-arm64 && scripts/release.sh carplayd > out/deploy-build.log 2>&1; s=$?; tail -3 out/deploy-build.log; [ $s -eq 0 ] && [ -s out/carplayd-linux-arm64 ]' || { echo "build failed on $BUILD (see ~/builds/carplay-deploy/out/deploy-build.log); nothing deployed" >&2; exit 1; }
scp -q "$BUILD:builds/carplay-deploy/out/carplayd-linux-arm64" "$TMP/carplayd"

# The F32 is car B in secrets (BMW#####), the F22 car A (BMW#####); carplayd
# calls the F32 "A" (priority) and the F22 "B".
umask 077
cat > "$TMP/carplayd.conf" <<CONF
phy = phy0
sta = wlan0
uplink = eth0
nat66 = nft
firewall = nft
events = /var/lib/carplayd/events.log
survey = ${SURVEY:-2s}
# Leave while the link is still usable; a missed goodbye strands the phone
# at most 12 s (3 x the 4 s max RA interval).
leave_for = 2s
router_lifetime = 12s
metrics = :9111

# DEBUG=on during a drive assessment: every wpa event, scan, station dump,
# neighbour table and leave decision. Off by default -- it writes roughly a
# line a second per car, which is a lot of SD card for a box left running.
debug = ${DEBUG:-off}

# Thresholds are learned per car from how each link actually behaves. Absolute
# dBm cannot be fixed here: the box sits behind a cinderblock wall 6-11 ft from
# the cars, so parked and dying signal overlap, and every number moves when a
# car parks somewhere new or a radio is swapped. The values below are the
# fallback used until a car has 120 healthy samples.
learn = ${LEARN:-on}
state = /var/lib/carplayd/learned.json

[car A]
ssid = $CAR_B_SSID
psk = $CAR_B_PSK
channel = 11
priority = 1
join = -80
leave = -85
rejoin = -77
drop = 6
${CAR_A_PHY:+phy = $CAR_A_PHY}
${CAR_A_STA:+sta = $CAR_A_STA}

[car B]
ssid = $CAR_A_SSID
psk = $CAR_A_PSK
channel = 11
prefix = fd5c:ba1e:f32:b::/64
join = -80
leave = -85
rejoin = -77
drop = 6
${CAR_B_PHY:+phy = $CAR_B_PHY}
${CAR_B_STA:+sta = $CAR_B_STA}
CONF

scp -q "$TMP/carplayd" "$TMP/carplayd.conf" "$HERE/carplayd/dist/carplayd.service" "$SBC:/tmp/"
ssh "$SBC" 'set -e
# carplayd isolates the car link and NATs with its own nftables table.
[ -x /usr/sbin/nft ] || sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -q nftables >/dev/null
sudo install -m 755 /tmp/carplayd /usr/local/sbin/carplayd
sudo install -m 600 /tmp/carplayd.conf /etc/carplayd.conf
sudo install -m 644 /tmp/carplayd.service /etc/systemd/system/carplayd.service
# Lab survey writes ~1 line per car per 10 s: rotate daily, keep 14 days.
# On the SD card, not /var/log: Armbian keeps /var/log in RAM (zram), which
# a power cut loses.
sudo mkdir -p /var/lib/carplayd; [ -f /var/log/carplayd.events ] && sudo sh -c "cat /var/log/carplayd.events >> /var/lib/carplayd/events.log && rm /var/log/carplayd.events" || true
printf "/var/lib/carplayd/events.log {\n\tdaily\n\trotate 14\n\tcompress\n\tdelaycompress\n\tmissingok\n\tnotifempty\n\tcopytruncate\n}\n" | sudo tee /etc/logrotate.d/carplayd >/dev/null
rm -f /tmp/carplayd /tmp/carplayd.conf /tmp/carplayd.service
/usr/local/sbin/carplayd -c /etc/carplayd.conf -check >/dev/null 2>&1 || sudo /usr/local/sbin/carplayd -c /etc/carplayd.conf -check | tail -3
[ -x ~/carplay-poc/carplay-box.sh ] && (cd ~/carplay-poc && sudo ./carplay-box.sh uninstall) || true
printf "[keyfile]\nunmanaged-devices=interface-name:wlan0\n" | sudo tee /etc/NetworkManager/conf.d/90-carplayd.conf >/dev/null
sudo nmcli general reload; sudo nmcli dev set wlan0 managed no 2>/dev/null || true
sudo systemctl daemon-reload
sudo systemctl enable carplayd >/dev/null 2>&1
sudo systemctl restart carplayd
sleep 3
systemctl is-active carplayd
sudo tail -4 /var/lib/carplayd/events.log | sed -E "s/BMW[0-9]{5}/BMW#####/g"
sudo /usr/sbin/nft list table inet carplayd | grep -cE "reject|drop|masquerade" | sed "s/^/nft rules: /"
curl -s localhost:9111 | grep -E "^carplayd_(serving|signal|phones|upstream)"'
echo "deployed. rollback: $0 rollback"
