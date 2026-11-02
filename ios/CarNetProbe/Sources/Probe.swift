// What iOS thinks of the CarPlay Wi-Fi, measured from the phone.
//
// Path monitors show whether iOS treats Wi-Fi as usable (and which gateways it
// learned). Probes fetch the same URLs twice: Wi-Fi only, and the way any app
// would. If Wi-Fi-only works but the default fails, the network is fine and
// iOS policy is the problem. getaddrinfo shows whether iOS asks for AAAA
// records on a ULA-only network. Bonjour finds the head unit's address.
// Everything goes to a log file you can share (keep it in secrets/).
import CoreLocation
import Foundation
import Network
import NetworkExtension
import Observation
import UIKit

@MainActor
@Observable
final class Probe {
	var paths: [String: String] = [:]
	var wifi = "not read yet"
	var addresses: [String] = []
	var results: [String] = []
	var bonjour: [String] = []
	var log: [String] = []
	var running = false
	var watching = false

	let logURL: URL = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
		.appendingPathComponent("carnetprobe.log")

	@ObservationIgnored private var started = false
	@ObservationIgnored private var monitors: [NWPathMonitor] = []
	@ObservationIgnored private var browsers: [NWBrowser] = []
	@ObservationIgnored private var resolvers: [Resolver] = []
	@ObservationIgnored private var watchTask: Task<Void, Never>?
	@ObservationIgnored private let location = CLLocationManager()
	@ObservationIgnored private let queue = DispatchQueue(label: "probe")

	static let targets: [(String, String)] = [
		("captive", "http://captive.apple.com/hotspot-detect.html"),   // iOS's own internet check
		("google maps", "https://maps.googleapis.com/maps/api/js"),
		("apple maps", "https://gsp-ssl.ls.apple.com/"),
		("v6 literal", "https://[2606:4700:4700::1111]/cdn-cgi/trace"), // ip= shows our egress address
		("v4 literal", "https://1.1.1.1/cdn-cgi/trace"),               // needs IPv4 or CLAT
		("v4-only host", "https://api.mapbox.com/"),                      // needs DNS64 + NAT64
	]
	static let dnsHosts = ["maps.googleapis.com", "api.mapbox.com", "ipv4only.arpa"]
	static let services = ["_carplay-ctrl._tcp", "_airplay._tcp", "_raop._tcp"]

	func start() {
		guard !started else { return }
		started = true
		note("start: \(UIDevice.current.systemName) \(UIDevice.current.systemVersion)")
		let kinds: [(String, NWInterface.InterfaceType?)] = [("any (what apps get)", nil), ("wifi", .wifi), ("cellular", .cellular)]
		for (label, type) in kinds {
			let m = type.map { NWPathMonitor(requiredInterfaceType: $0) } ?? NWPathMonitor()
			m.pathUpdateHandler = { [weak self] path in
				let s = Probe.describe(path)
				Task { @MainActor in self?.pathChanged(label, s) }
			}
			m.start(queue: queue)
			monitors.append(m)
		}
		location.requestWhenInUseAuthorization()
		browse()
		snapshot()
		// `xcrun devicectl device process launch ... app.carplaywan.probe -runProbes`
		if CommandLine.arguments.contains("-runProbes") { runProbes() }
	}

	func snapshot() {
		addresses = Probe.interfaceAddresses()
		addresses.forEach { note("addr: \($0)") }
		NEHotspotNetwork.fetchCurrent { net in
			let s = net.map {
				"\(Probe.redact($0.ssid)) bssid=\($0.bssid.prefix(8)):… signal=\(String(format: "%.2f", $0.signalStrength)) secure=\($0.isSecure)"
			} ?? "none (needs location access, or iOS hides CarPlay networks)"
			Task { @MainActor in
				self.wifi = s
				self.note("wifi: \(s)")
			}
		}
	}

	func runProbes() {
		running = true
		results = []
		snapshot()
		Task {
			for h in Probe.dnsHosts {
				let r = await Task.detached { Probe.resolve(h) }.value
				results.append("dns " + r)
				note("dns " + r)
			}
			let b = await Probe.boundCaptive()
			results.append(b)
			note(b)
			for (name, url) in Probe.targets {
				for wifiOnly in [true, false] {
					let r = await Probe.fetch(name, url, wifiOnly: wifiOnly)
					results.append(r)
					note(r)
				}
			}
			running = false
		}
	}

	// Watch: every 10 s, iOS's own check over Wi-Fi only and the default path.
	// Leave it running for the P4 exit test (screen stays on).
	func setWatching(_ on: Bool) {
		watching = on
		UIApplication.shared.isIdleTimerDisabled = on
		watchTask?.cancel()
		guard on else { note("watch off"); return }
		note("watch on")
		watchTask = Task {
			while !Task.isCancelled {
				let b = await Probe.boundCaptive()
				let w = await Probe.fetch("captive", Probe.targets[0].1, wifiOnly: true)
				let d = await Probe.fetch("captive", Probe.targets[0].1, wifiOnly: false)
				note("watch " + b)
				note("watch " + w)
				note("watch " + d)
				try? await Task.sleep(for: .seconds(10))
			}
		}
	}

	private func pathChanged(_ label: String, _ s: String) {
		paths[label] = s
		note("path \(label): \(s)")
		addresses = Probe.interfaceAddresses()
	}

	private func browse() {
		for type in Probe.services {
			let b = NWBrowser(for: .bonjour(type: type, domain: nil), using: NWParameters())
			b.browseResultsChangedHandler = { [weak self] found, _ in
				let items: [(String, String, String)] = found.compactMap { r in
					guard case let .service(name, type, _, _) = r.endpoint else { return nil }
					return (name, type, r.interfaces.map(\.name).joined(separator: ","))
				}
				Task { @MainActor in self?.found(items) }
			}
			b.stateUpdateHandler = { [weak self] st in
				Task { @MainActor in self?.note("browse \(type): \(st)") }
			}
			b.start(queue: queue)
			browsers.append(b)
		}
	}

	private func found(_ items: [(String, String, String)]) {
		for (name, type, ifs) in items {
			let key = "\(type) \(name)"
			guard !bonjour.contains(where: { $0.hasPrefix(key) }) else { continue }
			bonjour.append("\(key) on \(ifs)")
			note("bonjour: \(key) on \(ifs)")
			// Resolving asks mDNS for the address; it doesn't connect to the service.
			resolvers.append(Resolver(name: name, type: type) { [weak self] s in
				Task { @MainActor in
					guard let self else { return }
					if let i = self.bonjour.firstIndex(where: { $0.hasPrefix(key) }) { self.bonjour[i] = "\(key) on \(ifs): \(s)" }
					self.note("resolve \(key): \(s)")
				}
			})
		}
	}

	func note(_ s: String) {
		let f = DateFormatter()
		f.dateFormat = "HH:mm:ss.SSS"
		let line = "\(f.string(from: Date())) \(s)"
		log.append(line)
		let data = Data((line + "\n").utf8)
		if let h = try? FileHandle(forWritingTo: logURL) {
			h.seekToEndOfFile()
			h.write(data)
			try? h.close()
		} else {
			try? data.write(to: logURL)
		}
	}

	// MARK: - Helpers (off the main actor)

	nonisolated static func describe(_ p: Network.NWPath) -> String {
		var parts = ["\(p.status)"]
		if p.status != .satisfied { parts.append("reason=\(p.unsatisfiedReason)") }
		parts.append("v4=\(p.supportsIPv4) v6=\(p.supportsIPv6) dns=\(p.supportsDNS)")
		if p.isExpensive { parts.append("expensive") }
		if p.isConstrained { parts.append("constrained") }
		parts.append("ifs=" + p.availableInterfaces.map { "\($0.name)(\($0.type))" }.joined(separator: ","))
		if !p.gateways.isEmpty { parts.append("gw=" + p.gateways.map { "\($0)" }.joined(separator: ",")) }
		return parts.joined(separator: " ")
	}

	nonisolated static func fetch(_ name: String, _ url: String, wifiOnly: Bool) async -> String {
		let cfg = URLSessionConfiguration.ephemeral
		cfg.allowsCellularAccess = !wifiOnly
		cfg.timeoutIntervalForRequest = 8
		cfg.waitsForConnectivity = false
		let session = URLSession(configuration: cfg)
		defer { session.finishTasksAndInvalidate() }
		let metrics = MetricsCollector()
		let tag = "\(name) [\(wifiOnly ? "wifi-only" : "default")]"
		let start = Date()
		do {
			let (data, resp) = try await session.data(from: URL(string: url)!, delegate: metrics)
			let ms = Int(Date().timeIntervalSince(start) * 1000)
			let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
			let body = String(decoding: data, as: UTF8.self)
			var extra = ""
			if url.contains("hotspot-detect") { extra = body.contains("Success") ? "Success" : "NOT Success" }
			if let ip = body.split(separator: "\n").first(where: { $0.hasPrefix("ip=") }) { extra = String(ip) }
			return "\(tag) HTTP \(code) \(ms)ms \(extra) \(metrics.summary)"
		} catch {
			let e = error as NSError
			return "\(tag) FAIL \(e.code) \(e.localizedDescription) \(metrics.summary)"
		}
	}

	// iOS's internet check, bound to Wi-Fi (IP_BOUND_IF). configd gives a
	// service ranked Never/Scoped only a scoped default route: bound traffic
	// gets through, everyone else's doesn't. So bound OK + default FAIL means
	// the network works and iOS keeps apps off it.
	nonisolated static func boundCaptive() async -> String {
		let params = NWParameters.tcp
		params.requiredInterfaceType = .wifi
		let conn = NWConnection(host: "captive.apple.com", port: 80, using: params)
		let start = Date()
		let result: String = await withCheckedContinuation { cont in
			let once = Once(cont)
			conn.stateUpdateHandler = { st in
				switch st {
				case .ready:
					let req = "GET /hotspot-detect.html HTTP/1.0\r\nHost: captive.apple.com\r\n\r\n"
					conn.send(content: Data(req.utf8), completion: .contentProcessed { _ in })
					conn.receive(minimumIncompleteLength: 1, maximumLength: 8192) { data, _, _, err in
						let body = data.map { String(decoding: $0, as: UTF8.self) } ?? ""
						let local = conn.currentPath?.localEndpoint.map { "\($0)" } ?? "?"
						let remote = conn.currentPath?.remoteEndpoint.map { "\($0)" } ?? "?"
						once.resume(body.contains("Success")
							? "Success local=\(local) remote=\(remote)"
							: "NOT Success \(err.map { "\($0)" } ?? "") local=\(local)")
					}
				case .failed(let e): once.resume("FAIL \(e)")
				case .waiting(let e): once.resume("WAITING \(e)")
				default: break
				}
			}
			conn.start(queue: DispatchQueue(label: "bound"))
			DispatchQueue.global().asyncAfter(deadline: .now() + 8) { once.resume("TIMEOUT") }
		}
		conn.cancel()
		return "captive [wifi-bound] \(Int(Date().timeIntervalSince(start) * 1000))ms \(result)"
	}

	// AI_ADDRCONFIG is what apps use: it tells whether iOS asks for AAAA at all.
	nonisolated static func resolve(_ host: String) -> String {
		var hints = addrinfo()
		hints.ai_flags = AI_V4MAPPED_CFG | AI_ADDRCONFIG
		hints.ai_family = AF_UNSPEC
		hints.ai_socktype = SOCK_STREAM
		var res: UnsafeMutablePointer<addrinfo>?
		let rc = getaddrinfo(host, "443", &hints, &res)
		guard rc == 0, let first = res else { return "\(host): error \(String(cString: gai_strerror(rc)))" }
		defer { freeaddrinfo(res) }
		var addrs = Set<String>()
		for p in sequence(first: first, next: { $0.pointee.ai_next }) {
			if let s = numeric(p.pointee.ai_addr, p.pointee.ai_addrlen) { addrs.insert(s) }
		}
		return "\(host): " + addrs.sorted().joined(separator: " ")
	}

	nonisolated static func interfaceAddresses() -> [String] {
		var head: UnsafeMutablePointer<ifaddrs>?
		guard getifaddrs(&head) == 0, let first = head else { return [] }
		defer { freeifaddrs(head) }
		var out: [String] = []
		for p in sequence(first: first, next: { $0.pointee.ifa_next }) {
			guard let sa = p.pointee.ifa_addr else { continue }
			let fam = Int32(sa.pointee.sa_family)
			guard fam == AF_INET || fam == AF_INET6 else { continue }
			let name = String(cString: p.pointee.ifa_name)
			guard name.hasPrefix("en") || name.hasPrefix("pdp_ip") else { continue }
			let len = socklen_t(fam == AF_INET ? MemoryLayout<sockaddr_in>.size : MemoryLayout<sockaddr_in6>.size)
			if let a = numeric(sa, len) { out.append("\(name) \(a) \(classify(a))") }
		}
		return out
	}

	nonisolated static func numeric(_ sa: UnsafePointer<sockaddr>?, _ len: socklen_t) -> String? {
		guard let sa else { return nil }
		var host = [CChar](repeating: 0, count: Int(NI_MAXHOST))
		guard getnameinfo(sa, len, &host, socklen_t(host.count), nil, 0, NI_NUMERICHOST) == 0 else { return nil }
		return String(cString: host)
	}

	nonisolated static func classify(_ a: String) -> String {
		let l = a.lowercased()
		if l.hasPrefix("fe80") { return "link-local" }
		if l.hasPrefix("fd") || l.hasPrefix("fc") { return "ULA (box?)" }
		if l.contains(":") { return "global v6" }
		if l.hasPrefix("172.16.224.") { return "car net" }
		return "v4"
	}

	// SSID digits are the end of the VIN.
	nonisolated static func redact(_ s: String) -> String {
		s.replacingOccurrences(of: #"\d{5}"#, with: "#####", options: .regularExpression)
	}
}

// Resumes a continuation once, whichever of reply / failure / timeout comes first.
final class Once: @unchecked Sendable {
	private let lock = NSLock()
	private var cont: CheckedContinuation<String, Never>?

	init(_ cont: CheckedContinuation<String, Never>) { self.cont = cont }

	func resume(_ s: String) {
		lock.lock()
		let c = cont
		cont = nil
		lock.unlock()
		c?.resume(returning: s)
	}
}

final class MetricsCollector: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
	var summary = ""

	func urlSession(_ session: URLSession, task: URLSessionTask, didFinishCollecting metrics: URLSessionTaskMetrics) {
		guard let t = metrics.transactionMetrics.last else { return }
		summary = "local=\(t.localAddress ?? "?") remote=\(t.remoteAddress ?? "?") cellular=\(t.isCellular)"
	}
}

final class Resolver: NSObject, NetServiceDelegate {
	private let service: NetService
	private let done: (String) -> Void

	init(name: String, type: String, done: @escaping (String) -> Void) {
		service = NetService(domain: "local.", type: type + ".", name: name)
		self.done = done
		super.init()
		service.delegate = self
		service.resolve(withTimeout: 5)
	}

	func netServiceDidResolveAddress(_ sender: NetService) {
		let addrs = (sender.addresses ?? []).compactMap { d in
			d.withUnsafeBytes { Probe.numeric($0.baseAddress?.assumingMemoryBound(to: sockaddr.self), socklen_t(d.count)) }
		}
		done("host=\(sender.hostName ?? "?") " + addrs.joined(separator: " "))
	}

	func netService(_ sender: NetService, didNotResolve errorDict: [String: NSNumber]) {
		done("resolve failed \(errorDict)")
	}
}
