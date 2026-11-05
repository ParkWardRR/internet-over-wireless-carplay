// Drive mode: one JSONL file per drive, for the "CarPlay drops on departure"
// investigation and the review's recovery clocks (docs/recon-box.md).
//
// Every line: {"t": ISO time, "up": seconds since boot, "ev": ..., ...}
//   carplay   audio route to the car on/off (the public sign CarPlay is up)
//   path      any / wifi / cellular path changes, incl. the box's gateway
//   probe     every 2 s, a fresh DNS + TCP + TLS connect to www.google.com:443
//             over the default path, Wi-Fi only, and cellular only
//   loc       position once outside (none underground; that's expected)
//   app       foreground / background
//
// Location updates keep the app running in the background, so leave drive
// mode on, lock the phone and drive. Files: Documents/drive-*.jsonl, pulled
// with scripts/pull-drive-logs.sh (they hold your position: keep them in
// secrets/).
import AVFoundation
import CoreLocation
import Foundation
import Network
import Observation
import UIKit

@MainActor
@Observable
final class DriveLog: NSObject, CLLocationManagerDelegate {
	var on = false
	var carplay = "unknown"
	var last: [String: String] = [:] // probe label → last result, for the UI
	var lines = 0
	var file: URL?

	@ObservationIgnored private let loc = CLLocationManager()
	@ObservationIgnored private var monitors: [NWPathMonitor] = []
	@ObservationIgnored private var tasks: [Task<Void, Never>] = []
	@ObservationIgnored private var handle: FileHandle?
	@ObservationIgnored private var observers: [NSObjectProtocol] = []
	@ObservationIgnored private let queue = DispatchQueue(label: "drivelog")
	@ObservationIgnored private let iso: ISO8601DateFormatter = {
		let f = ISO8601DateFormatter()
		f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
		return f
	}()

	func toggle(_ want: Bool) { want ? start() : stop() }

	func start() {
		guard !on else { return }
		on = true
		let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
		let name = "drive-" + iso.string(from: Date()).replacingOccurrences(of: ":", with: "") + ".jsonl"
		file = docs.appendingPathComponent(name)
		FileManager.default.createFile(atPath: file!.path, contents: nil)
		handle = try? FileHandle(forWritingTo: file!)
		lines = 0
		write("start", ["os": "\(UIDevice.current.systemName) \(UIDevice.current.systemVersion)", "model": Self.model()])

		// Location: keeps us alive in the background and gives the exit route.
		loc.delegate = self
		loc.desiredAccuracy = kCLLocationAccuracyBest
		loc.distanceFilter = 5
		loc.pausesLocationUpdatesAutomatically = false
		loc.requestAlwaysAuthorization()
		loc.allowsBackgroundLocationUpdates = true
		loc.showsBackgroundLocationIndicator = true
		loc.startUpdatingLocation()

		// CarPlay: the audio route goes to .carAudio while CarPlay is connected.
		try? AVAudioSession.sharedInstance().setCategory(.ambient, options: [.mixWithOthers])
		noteRoute("start")
		observers.append(NotificationCenter.default.addObserver(forName: AVAudioSession.routeChangeNotification, object: nil, queue: .main) { [weak self] n in
			let why = (n.userInfo?[AVAudioSessionRouteChangeReasonKey] as? UInt).flatMap { AVAudioSession.RouteChangeReason(rawValue: $0) }
			MainActor.assumeIsolated { self?.noteRoute(why.map { "\($0.rawValue)" } ?? "?") }
		})
		for (n, s) in [(UIApplication.didEnterBackgroundNotification, "background"), (UIApplication.willEnterForegroundNotification, "foreground")] {
			observers.append(NotificationCenter.default.addObserver(forName: n, object: nil, queue: .main) { [weak self] _ in
				MainActor.assumeIsolated { self?.write("app", ["state": s]) }
			})
		}

		for (label, type) in [("any", nil), ("wifi", NWInterface.InterfaceType.wifi), ("cellular", .cellular)] as [(String, NWInterface.InterfaceType?)] {
			let m = type.map { NWPathMonitor(requiredInterfaceType: $0) } ?? NWPathMonitor()
			m.pathUpdateHandler = { [weak self] p in
				let f: [String: String] = [
					"which": label, "status": "\(p.status)",
					"ifs": p.availableInterfaces.map { "\($0.name)(\($0.type))" }.joined(separator: ","),
					"gw": p.gateways.map { "\($0)" }.joined(separator: ","),
					"v4": "\(p.supportsIPv4)", "v6": "\(p.supportsIPv6)", "dns": "\(p.supportsDNS)",
					"expensive": "\(p.isExpensive)",
				]
				Task { @MainActor in
					self?.write("path", f)
					if label == "wifi" { self?.write("addrs", ["wifi": Probe.interfaceAddresses().filter { $0.hasPrefix("en0") }.joined(separator: " ")]) }
				}
			}
			m.start(queue: queue)
			monitors.append(m)
		}

		// Fresh connections every 2 s, over each path.
		for (label, type) in [("default", nil), ("wifi", NWInterface.InterfaceType.wifi), ("cellular", .cellular)] as [(String, NWInterface.InterfaceType?)] {
			tasks.append(Task { [weak self] in
				while !Task.isCancelled {
					let r = await DriveLog.connect(type)
					await MainActor.run {
						self?.last[label] = r["result"].map { "\($0) \(r["ms"] ?? "")ms \(r["if"] ?? "")" }
						self?.write("probe", r.merging(["path": label]) { a, _ in a })
					}
					try? await Task.sleep(for: .seconds(2))
				}
			})
		}
	}

	func stop() {
		guard on else { return }
		write("stop", [:])
		on = false
		tasks.forEach { $0.cancel() }
		tasks = []
		monitors.forEach { $0.cancel() }
		monitors = []
		observers.forEach { NotificationCenter.default.removeObserver($0) }
		observers = []
		loc.stopUpdatingLocation()
		loc.allowsBackgroundLocationUpdates = false
		try? handle?.close()
		handle = nil
	}

	private func noteRoute(_ why: String) {
		let outs = AVAudioSession.sharedInstance().currentRoute.outputs
		let car = outs.contains { $0.portType == .carAudio }
		let now = car ? "on" : "off"
		write("route", ["why": why, "outputs": outs.map { "\($0.portType.rawValue):\($0.portName)" }.joined(separator: ",")])
		if now != carplay {
			carplay = now
			write("carplay", ["state": now])
		}
	}

	private func write(_ ev: String, _ fields: [String: String]) {
		var obj: [String: Any] = ["t": iso.string(from: Date()), "up": ProcessInfo.processInfo.systemUptime, "ev": ev]
		fields.forEach { obj[$0.key] = $0.value }
		guard let data = try? JSONSerialization.data(withJSONObject: obj, options: [.sortedKeys]) else { return }
		handle?.write(data + Data("\n".utf8))
		lines += 1
	}

	nonisolated func locationManager(_ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]) {
		let f = locations.map { l in
			[
				"lat": String(format: "%.6f", l.coordinate.latitude), "lon": String(format: "%.6f", l.coordinate.longitude),
				"acc": String(format: "%.0f", l.horizontalAccuracy), "speed": String(format: "%.1f", l.speed),
			]
		}
		Task { @MainActor in f.forEach { self.write("loc", $0) } }
	}

	nonisolated func locationManager(_ manager: CLLocationManager, didFailWithError error: Error) {
		let s = "\(error)"
		Task { @MainActor in self.write("loc_error", ["error": s]) }
	}

	// A fresh DNS + TCP + TLS connection, nothing cached: what a new request
	// from Maps would need. 3 s budget.
	nonisolated static func connect(_ type: NWInterface.InterfaceType?) async -> [String: String] {
		let params = NWParameters(tls: .init(), tcp: .init())
		if let type { params.requiredInterfaceType = type }
		let conn = NWConnection(host: "www.google.com", port: 443, using: params)
		let start = Date()
		let done = OnceFlag()
		let q = DispatchQueue(label: "drivelog.connect")
		return await withCheckedContinuation { (k: CheckedContinuation<[String: String], Never>) in
			@Sendable func finish(_ result: String, _ extra: [String: String] = [:]) {
				done.run {
					var r = extra
					r["result"] = result
					r["ms"] = "\(Int(Date().timeIntervalSince(start) * 1000))"
					r["if"] = conn.currentPath?.availableInterfaces.first.map { "\($0.name)" } ?? ""
					conn.cancel()
					k.resume(returning: r)
				}
			}
			conn.stateUpdateHandler = { s in
				switch s {
				case .ready: finish("ok")
				case .failed(let e): finish("fail", ["error": "\(e)"])
				case .waiting(let e): finish("waiting", ["error": "\(e)"])
				default: break
				}
			}
			conn.start(queue: q)
			q.asyncAfter(deadline: .now() + 3) { finish("timeout") }
		}
	}

	nonisolated static func model() -> String {
		var u = utsname()
		uname(&u)
		return withUnsafeBytes(of: &u.machine) { String(decoding: $0.prefix { $0 != 0 }, as: UTF8.self) }
	}
}

// Runs its block at most once, from any thread.
final class OnceFlag: @unchecked Sendable {
	private let lock = NSLock()
	private var done = false
	func run(_ f: () -> Void) {
		lock.lock()
		defer { lock.unlock() }
		guard !done else { return }
		done = true
		f()
	}
}
