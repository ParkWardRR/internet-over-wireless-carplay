#!/bin/sh
# Deploy carplayd to garage-sbc (Debian 12, brcmfmac, NetworkManager), from the
# Mac. Builds on build-host, copies the arm64 binary over, writes /etc/carplayd.conf
# from secrets/carplay.env, takes wlan0 away from NetworkManager, removes the
# interim carplay-box.sh setup and starts carplayd.service.
#
#   scripts/deploy-carplayd-sbc.sh            deploy / update
#   scripts/deploy-carplayd-sbc.sh rollback   back to carplay-box.sh (NetworkManager)
#
# Thresholds are lab-loose (join -88 / leave -92 / rejoin -85) until the signal
# survey from the SBC's spot (docs/two-cars.md, B2).
set -eu
HERE=$(cd "$(dirname "$0")/.." && pwd)
SBC=${SBC:-user@garage-sbc.local}
BUILD=${BUILD:-user@build-host.local}
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if [ "${1:-}" = rollback ]; then
	ssh "$SBC" 'sudo systemctl disable --now carplayd 2>/dev/null; sudo rm -f /etc/NetworkManager/conf.d/90-carplayd.conf; sudo nmcli general reload; sudo nmcli dev set wlan0 managed yes; cd ~/carplay-poc && sudo ./carplay-box.sh install'
	exit 0
fi

. "$HERE/secrets/carplay.env"
[ -n "${CAR_B_SSID:-}" ] && [ -n "${CAR_B_PSK:-}" ] && [ -n "${CAR_A_SSID:-}" ] && [ -n "${CAR_A_PSK:-}" ] \
	|| { echo "secrets/carplay.env needs CAR_A_* and CAR_B_*" >&2; exit 1; }

echo "building on build-host..."
rsync -a --delete --exclude secrets/ --exclude .git/ --exclude emu/out/ --exclude 'ios/CarNetProbe/*.xcodeproj' \
	"$HERE/" "$BUILD:builds/carplay-deploy/"
ssh "$BUILD" 'mkdir -p ~/builds/carplay-deploy && cd ~/builds/carplay-deploy && emu/run.sh carplayd' | tail -1
scp -q "$BUILD:builds/carplay-deploy/emu/out/carplayd-arm64" "$TMP/carplayd"

# The F32 is car B in secrets (BMW#####), the F22 car A (BMW#####); carplayd
# calls the F32 "A" (priority) and the F22 "B".
umask 077
cat > "$TMP/carplayd.conf" <<CONF
phy = phy0
sta = wlan0
uplink = eth0
nat66 = ip6tables
events = /var/log/carplayd.events
metrics = :9111

[car A]
ssid = $CAR_B_SSID
psk = $CAR_B_PSK
channel = 11
priority = 1
join = -88
leave = -92
rejoin = -85

[car B]
ssid = $CAR_A_SSID
psk = $CAR_A_PSK
channel = 0
join = -88
leave = -92
rejoin = -85
CONF

scp -q "$TMP/carplayd" "$TMP/carplayd.conf" "$HERE/carplayd/dist/carplayd.service" "$SBC:/tmp/"
ssh "$SBC" 'set -e
sudo install -m 755 /tmp/carplayd /usr/local/sbin/carplayd
sudo install -m 600 /tmp/carplayd.conf /etc/carplayd.conf
sudo install -m 644 /tmp/carplayd.service /etc/systemd/system/carplayd.service
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
sudo tail -4 /var/log/carplayd.events | sed -E "s/BMW[0-9]{5}/BMW#####/g"
curl -s localhost:9111 | grep -E "^carplayd_(serving|signal|phones)"'
echo "deployed. rollback: $0 rollback"
