#!/bin/sh
# Emulator entry point on build-host (inside ~/builds/bmw-f32-carplay-wan):
#   emu/run.sh build            build emu/out/openwrt.img (OpenWrt snapshot + hwsim)
#   emu/run.sh boot             boot it under KVM in the background (SSH on :2222)
#   emu/run.sh ssh [cmd]        run a command in the VM
#   emu/run.sh console <cmd>    type a command on the serial console (works when the VM network is broken)
#   emu/run.sh test [-scenarios a,b] [-n N]   run the Go scenario harness (fresh VM per run)
#   emu/run.sh stop             power it off
set -eu
cd "$(dirname "$0")/.."
REPO=$(pwd)
CACHE=$HOME/builds/.cache
KEY=$CACHE/carplay-emu/id_ed25519
PORT=${EMU_SSH_PORT:-2290}
NAME=carplay-emu-vm

case ${1:-} in
build)
	mkdir -p "$CACHE/go" emu/out
	podman run --rm -v "$REPO:/work:Z" -v "$CACHE/go:/go:Z" -w /work/emu/agent -e CGO_ENABLED=0 docker.io/library/golang:1.25 go build -trimpath -o /work/emu/out/emuagent .
	podman run --rm -v "$REPO:/work:Z" -v "$CACHE:/cache:Z" carplay-emu sh /work/emu/build-image.sh ;;
boot)
	cp emu/out/openwrt.img emu/out/run.img
	podman rm -f "$NAME" >/dev/null 2>&1 || true
	podman run -d --name "$NAME" --device /dev/kvm -p "127.0.0.1:$PORT:2222" -v "$REPO/emu/out:/vm:Z" carplay-emu \
		qemu-system-x86_64 -enable-kvm -cpu host -smp 2 -m 1024 -nographic \
		-drive file=/vm/run.img,format=raw,if=virtio \
		-netdev user,id=mgmt,hostfwd=tcp::2222-:22 -device virtio-net-pci,netdev=mgmt \
		-chardev socket,id=con,path=/vm/console.sock,server=on,wait=off,logfile=/vm/console.log -serial chardev:con -monitor none >/dev/null
	i=0; until ssh -q -i "$KEY" -p "$PORT" -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=2 root@127.0.0.1 true 2>/dev/null; do
		i=$((i+1)); [ $i -lt 90 ] || { echo "VM did not come up; tail emu/out/console.log"; exit 1; }; sleep 1; done
	echo "VM up after ~${i}s (ssh -p $PORT root@127.0.0.1)" ;;
ssh)
	shift; exec ssh -q -i "$KEY" -p "$PORT" -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@127.0.0.1 "$@" ;;
console)
	shift; printf "%s\n" "$*" | podman exec -i "$NAME" socat -t 3 - UNIX-CONNECT:/vm/console.sock >/dev/null ;;
test)
	shift; mkdir -p "$CACHE/go" emu/out
	podman run --rm -v "$REPO:/work:Z" -v "$CACHE/go:/go:Z" -w /work/emu/harness -e CGO_ENABLED=0 docker.io/library/golang:1.25 go build -trimpath -o /work/emu/out/emuharness .
	exec emu/out/emuharness -repo "$REPO" "$@" ;;
stop)
	podman rm -f "$NAME" >/dev/null 2>&1 || true; echo stopped ;;
*) echo "usage: emu/run.sh build|boot|ssh [cmd]|stop" >&2; exit 2 ;;
esac
