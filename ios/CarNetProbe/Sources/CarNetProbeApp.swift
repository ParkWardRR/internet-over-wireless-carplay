import SwiftUI

@main
struct CarNetProbeApp: App {
	@State private var probe = Probe()
	@State private var drive = DriveLog()

	var body: some Scene {
		WindowGroup {
			ContentView(probe: probe, drive: drive)
		}
	}
}

struct ContentView: View {
	let probe: Probe
	let drive: DriveLog

	var body: some View {
		NavigationStack {
			List {
				Section("Drive mode") {
					Toggle("Log this drive", isOn: Binding(get: { drive.on }, set: { drive.toggle($0) }))
					Text("CarPlay: \(drive.carplay)   lines: \(drive.lines)").font(.caption.monospaced())
					ForEach(drive.last.keys.sorted(), id: \.self) { k in
						Text("\(k): \(drive.last[k] ?? "")").font(.caption.monospaced())
					}
					if let f = drive.file { Text(f.lastPathComponent).font(.caption2.monospaced()) }
				}
				Section("Paths (live)") {
					ForEach(probe.paths.keys.sorted(), id: \.self) { k in
						VStack(alignment: .leading) {
							Text(k).font(.caption).bold()
							Text(probe.paths[k] ?? "").font(.caption.monospaced())
						}
					}
				}
				Section("Wi-Fi") {
					Text(probe.wifi).font(.caption.monospaced())
				}
				Section("Addresses") {
					ForEach(probe.addresses, id: \.self) { Text($0).font(.caption.monospaced()) }
				}
				Section("Probes") {
					ForEach(probe.results, id: \.self) { Text($0).font(.caption.monospaced()) }
				}
				Section("Bonjour") {
					ForEach(probe.bonjour, id: \.self) { Text($0).font(.caption.monospaced()) }
				}
				Section("Log") {
					ForEach(Array(probe.log.suffix(200).reversed().enumerated()), id: \.offset) {
						Text($0.element).font(.caption2.monospaced())
					}
				}
			}
			.navigationTitle("CarNet Probe")
			.toolbar {
				ToolbarItem(placement: .topBarTrailing) {
					ShareLink(item: probe.logURL)
				}
			}
			.safeAreaInset(edge: .bottom) {
				HStack {
					Button("Run probes") { probe.runProbes() }
						.buttonStyle(.borderedProminent)
						.disabled(probe.running)
					Toggle("Watch", isOn: Binding(get: { probe.watching }, set: { probe.setWatching($0) }))
						.fixedSize()
				}
				.padding()
				.background(.bar)
			}
			.refreshable { probe.snapshot() }
		}
		.task { probe.start() }
	}
}
