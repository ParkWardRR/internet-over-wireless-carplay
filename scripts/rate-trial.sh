#!/bin/sh
# Find the right egress cap for a car link, by measuring rather than guessing.
#
#   sudo scripts/rate-trial.sh                 # 1, 2, 3, 4 mbit
#   sudo scripts/rate-trial.sh 1mbit 2mbit     # just these
#   sudo scripts/rate-trial.sh --secs 90 ...   # longer per step
#   sudo scripts/rate-trial.sh --include-off   # also test unshaped (see below)
#
# Per step it applies the rate, waits for you to load the link, and records
# loaded round-trip percentiles, throughput, shaper drops and whether the link
# survived. You supply the one thing the box cannot see: whether CarPlay's own
# audio stutters, because that stream never passes through the box.
#
# Results go to captures/rate-trial-<car>-<stamp>.tsv (gitignored) and a
# sanitized summary is printed for docs/bandwidth.md.
#
# --include-off is NOT the default on purpose. Unshaped saturation dropped the
# F22's CarPlay session on 2026-11-16 and needed a hard reset of the head unit
# to pair again. Only pass it if you are willing to do that again.
set -eu
PATH=$PATH:/sbin:/usr/sbin

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/.." && pwd)
OUTDIR=${CAPTURE_DIR:-$ROOT/captures}
SECS=60
RATES=""
INCLUDE_OFF=0

die() { echo "error: $*" >&2; exit 1; }
say() { echo "rate-trial: $*"; }

while [ $# -gt 0 ]; do
	case "$1" in
	--secs) SECS=$2; shift 2 ;;
	--include-off) INCLUDE_OFF=1; shift ;;
	-h | --help) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) RATES="$RATES $1"; shift ;;
	esac
done
[ -n "$RATES" ] || RATES="1mbit 2mbit 3mbit 4mbit"
[ "$INCLUDE_OFF" = 1 ] && RATES="$RATES off"

[ "$(id -u)" -eq 0 ] || die "run with sudo"
for c in iw tc ping6 ip; do command -v $c >/dev/null || die "$c is not installed"; done

# Which radio is serving, and which phone is on it.
STA=""
for s in cara carb car-a car-b wlan0; do
	[ -e "/sys/class/net/$s" ] || continue
	iw dev "$s" link 2>/dev/null | grep -q '^Connected' && { STA=$s; break; }
done
[ -n "$STA" ] || die "no car link is up; start the car and wait for carplayd to serve it"
SSID=$(iw dev "$STA" link | sed -n 's/.*SSID: //p' | sed -E 's/[0-9]{4,}/####/g')
PHONE=$(ip -6 neigh show dev "$STA" | awk '/^fd5c/ {print $1; exit}')
[ -n "$PHONE" ] || die "no phone on $STA yet; start CarPlay and let the phone take an address"

# carplayd in auto mode would fight us: it re-decides every 15 s.
if grep -q '^rate *= *auto' /etc/carplayd.conf 2>/dev/null; then
	say "WARNING: carplayd.conf has rate = auto, which re-decides every 15s and will"
	say "         overwrite each step. Set a fixed rate (or 'off') and restart carplayd"
	say "         before trusting these numbers."
	printf "continue anyway? [y/N] "
	read -r a
	[ "$a" = y ] || exit 1
fi

mkdir -p "$OUTDIR"; chmod 700 "$OUTDIR"
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
TSV="$OUTDIR/rate-trial-$STA-$STAMP.tsv"
printf "rate\tsecs\tdl_kBps_mean\tdl_kBps_peak\trtt_p50\trtt_p95\trtt_max\tdrops\toverlimits\tdisconnects\taudio\n" > "$TSV"

apply() {
	tc qdisc del dev "$STA" root 2>/dev/null || true
	[ "$1" = off ] && return 0
	tc qdisc add dev "$STA" root handle 1: htb default 10
	tc class add dev "$STA" parent 1: classid 1:10 htb rate "$1" ceil "$1"
	tc qdisc add dev "$STA" parent 1:10 handle 10: pfifo limit 16
}

say "car $SSID on $STA, phone found, ${SECS}s per step"
say "steps:$RATES"
echo

for RATE in $RATES; do
	[ "$RATE" = off ] && say "*** UNSHAPED STEP: this is the one that dropped CarPlay before ***"
	apply "$RATE"
	say "rate=$RATE applied. LOAD THE LINK NOW (big download + streaming), ${SECS}s..."

	tx0=$(cat "/sys/class/net/$STA/statistics/tx_bytes")
	rtts=$(mktemp); peak=0; prev=$tx0; i=0
	while [ "$i" -lt "$SECS" ]; do
		ping6 -c 4 -i 0.2 -W 1 -I "$STA" "$PHONE" 2>/dev/null |
			sed -n 's/.*time=\([0-9.]*\) ms.*/\1/p' >> "$rtts"
		now=$(cat "/sys/class/net/$STA/statistics/tx_bytes")
		d=$(( (now - prev) / 1024 ))
		[ "$d" -gt "$peak" ] && peak=$d
		prev=$now
		i=$((i + 1))
	done
	tx1=$(cat "/sys/class/net/$STA/statistics/tx_bytes")

	mean=$(( (tx1 - tx0) / 1024 / SECS ))
	p50=$(sort -n "$rtts" | awk '{a[NR]=$1} END{if(NR)printf "%.0f", a[int(NR*0.5)+0.5==0?1:int(NR*0.5)+1]}')
	p95=$(sort -n "$rtts" | awk '{a[NR]=$1} END{if(NR){i=int(NR*0.95+0.999); if(i<1)i=1; printf "%.0f", a[i]}}')
	mx=$(sort -n "$rtts" | tail -1)
	if [ "$RATE" = off ]; then
		drops=-; over=-
	else
		stats=$(tc -s class show dev "$STA" | sed -n 's/.*dropped \([0-9]*\), overlimits \([0-9]*\).*/\1 \2/p' | head -1)
		drops=${stats%% *}; over=${stats##* }
	fi
	conn=$(iw dev "$STA" link 2>/dev/null | grep -c '^Connected' || true)
	disc=$([ "$conn" = 1 ] && echo 0 || echo 1)

	echo
	printf "  %s: mean %s kB/s, peak %s kB/s, rtt p50=%sms p95=%sms max=%sms, drops=%s overlimits=%s link=%s\n" \
		"$RATE" "$mean" "$peak" "${p50:-?}" "${p95:-?}" "${mx:-?}" "$drops" "$over" \
		"$([ "$disc" = 0 ] && echo up || echo LOST)"
	printf "  Did CarPlay audio stutter at this rate? [n]one / [m]ild / [b]ad / [d]ropped: "
	read -r audio
	case "$audio" in n*) audio=none ;; m*) audio=mild ;; b*) audio=bad ;; d*) audio=dropped ;; *) audio="?" ;; esac

	printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n" \
		"$RATE" "$SECS" "$mean" "$peak" "${p50:-}" "${p95:-}" "${mx:-}" "$drops" "$over" "$disc" "$audio" >> "$TSV"
	rm -f "$rtts"
	echo
done

say "restoring carplayd's own cap by rejoining is cleanest; for now leaving the last step applied"
say "raw: $TSV"
echo
echo "Paste into docs/bandwidth.md:"
echo
awk -F'\t' 'NR==1{print "| rate | mean kB/s | peak kB/s | p50 | p95 | max | drops | overlimits | link | audio |";
  print "|---|---|---|---|---|---|---|---|---|---|"; next}
  {printf "| %s | %s | %s | %s ms | %s ms | %s ms | %s | %s | %s | %s |\n", $1,$3,$4,$5,$6,$7,$8,$9,($10=="0"?"up":"LOST"),$11}' "$TSV"
