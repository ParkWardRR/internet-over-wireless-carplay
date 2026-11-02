// P1: read what the car's CarPlay AP tells the iPhone in its beacon.
//
//   scripts/car-ie.sh          # scan, decode every Apple Device IE
//   scripts/car-ie.sh --all    # also list networks without one
//
// macOS only returns BSSIDs and raw IEs to apps with Location access, so
// car-ie.sh wraps this in a tiny app bundle that asks once.
//
// The Apple Device IE (vendor IE, OUI 00:A0:40 type 0, from Apple's
// AccessorySDK IEEE80211Utils.h) carries a flags bit array; bit 10 = CarPlay,
// bit 11 = "Provides Internet access". The 802.11u Interworking element
// (ID 107) has its own Internet bit. iOS reads these to decide whether the
// car Wi-Fi offers internet. No monitor mode needed: CoreWLAN hands back each
// scanned network's raw IEs. SSID digits (end of the VIN) are masked.
import AppKit
import CoreLocation
import CoreWLAN
import Foundation

final class LocationGate: NSObject, CLLocationManagerDelegate {
	let manager = CLLocationManager()
	var decided = false

	override init() {
		super.init()
		manager.delegate = self
	}

	func locationManagerDidChangeAuthorization(_ m: CLLocationManager) {
		decided = m.authorizationStatus != .notDetermined
	}

	func wait() {
		if manager.authorizationStatus == .notDetermined {
			// The prompt only shows for an active app.
			NSApplication.shared.setActivationPolicy(.accessory)
			NSApplication.shared.activate(ignoringOtherApps: true)
			manager.requestAlwaysAuthorization()
			let deadline = Date().addingTimeInterval(120)
			while !decided && Date() < deadline { RunLoop.main.run(until: Date().addingTimeInterval(0.2)) }
		}
		let s = manager.authorizationStatus
		if s != .authorizedAlways && s.rawValue != 4 {
			print("Location status \(s.rawValue) (0 = never asked, 2 = denied): macOS hides BSSIDs and IEs. Allow CarIE in System Settings > Privacy & Security > Location Services")
		}
	}
}

let flagNames = [
	"AirPlay", "Unconfigured", "MFiConfigV1", "WakeOnWireless", "InterferenceRobustness",
	"PPPoEServer", "WPSCapable", "WPSActive", "AirPrint", "iAPoverWiFi", "CarPlay",
	"InternetAccess", "ACPConfigV1", "UserContact", "2.4GHz", "5GHz", "PINConfig", "HomeKit",
]

func elements(_ d: Data) -> [(UInt8, Data)] {
	var out: [(UInt8, Data)] = []
	let b = [UInt8](d)
	var i = 0
	while i + 2 <= b.count {
		let id = b[i], len = Int(b[i + 1])
		guard i + 2 + len <= b.count else { break }
		out.append((id, Data(b[(i + 2)..<(i + 2 + len)])))
		i += 2 + len
	}
	return out
}

func hex(_ d: Data) -> String { d.map { String(format: "%02x", $0) }.joined() }

func describeAppleIE(_ body: Data) -> String {
	// body = <sub-id 1><len 1><value> ... after OUI + type
	var parts: [String] = []
	for (id, v) in elements(body) {
		switch id {
		case 0x00:
			var set: [String] = []
			for (byteIndex, byte) in v.enumerated() {
				for bit in 0..<8 where byte & (0x80 >> bit) != 0 {
					let n = byteIndex * 8 + bit
					set.append(n < flagNames.count ? flagNames[n] : "bit\(n)")
				}
			}
			let internet = set.contains("InternetAccess") ? "INTERNET BIT SET" : "internet bit clear"
			parts.append("flags=\(hex(v)) [\(set.joined(separator: ","))] \(internet)")
		case 0x01: parts.append("name=\(String(decoding: v, as: UTF8.self))")
		case 0x02: parts.append("manufacturer=\(String(decoding: v, as: UTF8.self))")
		case 0x03: parts.append("model=\(String(decoding: v, as: UTF8.self))")
		case 0x06: parts.append("bt-mac=<\(v.count) bytes>")
		case 0x07: parts.append("device-id=<\(v.count) bytes>")
		default: parts.append("sub\(id)=\(hex(v))")
		}
	}
	return parts.joined(separator: " ")
}

func describeInterworking(_ v: Data) -> String {
	guard let ano = v.first else { return "interworking=empty" }
	let types = ["private", "private+guest", "chargeable public", "free public", "personal device", "emergency only"]
	let t = Int(ano & 0x0f)
	return "interworking: type=\(t < types.count ? types[t] : "\(t)") internet=\(ano & 0x10 != 0) raw=\(hex(v))"
}

func mask(_ s: String?) -> String {
	guard let s else { return "<ssid hidden: grant Location to Terminal>" }
	return s.replacingOccurrences(of: #"\d{5}"#, with: "#####", options: .regularExpression)
}

// Decode canned beacon bytes: an Apple Device IE with AirPlay + CarPlay + 2.4 GHz
// (the flags luka-dev/mib2q-carplay-rgi sends), then an Interworking element
// with the Internet bit set (as gyqworld/liunx-CarPlay adds).
if CommandLine.arguments.contains("--selftest") {
	let blob = Data([0xdd, 0x08, 0x00, 0xa0, 0x40, 0x00, 0x00, 0x02, 0x80, 0x22, 0x6b, 0x01, 0x10])
	for (id, v) in elements(blob) {
		if id == 221 { print("apple-device-ie: " + describeAppleIE(v.dropFirst(4))) }
		if id == 107 { print(describeInterworking(v)) }
	}
	exit(0)
}

LocationGate().wait()
guard let iface = CWWiFiClient.shared().interface() else { print("no Wi-Fi interface"); exit(1) }
let showAll = CommandLine.arguments.contains("--all")
let networks: Set<CWNetwork>
do { networks = try iface.scanForNetworks(withSSID: nil) } catch { print("scan failed: \(error)"); exit(1) }

var found = 0
for n in networks.sorted(by: { $0.rssiValue > $1.rssiValue }) {
	guard let ies = n.informationElementData else { continue }
	var apple: String?
	var iw: String?
	for (id, v) in elements(ies) {
		if id == 221, v.count >= 4, v.starts(with: [0x00, 0xA0, 0x40]), v[v.startIndex + 3] == 0x00 {
			apple = describeAppleIE(v.dropFirst(4))
		}
		if id == 107 { iw = describeInterworking(v) }
	}
	guard apple != nil || showAll else { continue }
	found += 1
	let ch = n.wlanChannel.map { "ch \($0.channelNumber) \($0.channelBand == .band5GHz ? "5GHz" : $0.channelBand == .band2GHz ? "2.4GHz" : "6GHz")" } ?? "ch ?"
	print("\(mask(n.ssid))  \(ch)  rssi \(n.rssiValue)")
	print("  apple-device-ie: \(apple ?? "none")")
	print("  \(iw ?? "interworking: none")")
}
if found == 0 { print("no network with an Apple Device IE in range (car off, or too far?). --all lists everything.") }
