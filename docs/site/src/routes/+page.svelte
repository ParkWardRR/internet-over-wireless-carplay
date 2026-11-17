<script lang="ts">
	import Diagram from '$lib/scene/Diagram.svelte';

	const REPO = 'https://github.com/ParkWardRR/internet-over-wireless-carplay';
	const WIKI = `${REPO}/wiki`;
</script>

<svelte:head>
	<title>CarPlay can stay connected when the internet disappears</title>
</svelte:head>

<main>
	<header class="hero">
		<p class="eyebrow">BMW F32 and F22 · factory NBT EVO · iPhone on iOS 27</p>
		<h1>CarPlay can stay connected when the internet disappears.</h1>
		<p class="lede">
			Wireless CarPlay is the link between your phone and the dashboard; your internet is a
			separate link to somewhere else entirely — and underground, only one of them fails.
		</p>
	</header>

	<Diagram />

	<div class="detail">
		<section>
			<h2>The local CarPlay network</h2>
			<p>
				The head unit is an access point. It runs a small WPA2-PSK network on 2.4 GHz — channel
				11, HT20, one spatial stream on both cars tested — and hands out addresses on
				<code>172.16.224.0/24</code> with <strong>no router and no DNS</strong>. There is nowhere
				for traffic to go but the dashboard. <span class="tag">measured</span>
			</p>
			<p>
				It also advertises that fact: the beacon's 802.11u Interworking "Internet" bit is zero
				and there is no Apple Device information element at all. No box on the network can
				change what the car broadcasts about itself. <span class="tag">measured</span>
			</p>
			<p>
				Two things about it turn out to be load-bearing. There is <strong>no client
				isolation</strong>, so a second device may join and the head unit will relay between
				its clients. And the head unit sends <strong>no IPv6 router advertisements</strong> of
				its own. <span class="tag">measured</span>
			</p>
		</section>

		<section>
			<h2>How this project supplies an internet route</h2>
			<p>
				A small Linux box in the garage joins the car's network as an ordinary second station,
				one radio per car. It <strong>does not touch IPv4</strong>: it takes a DHCP lease like
				any other client and adds nothing there. The car owns IPv4 and keeps it.
			</p>
			<p>
				Instead it becomes the one thing the link is missing — <strong>the only IPv6 router on
				it</strong>. It advertises a private prefix, itself as the route, and a DNS server. The
				phone picks that up by plain SLAAC, with nothing configured on the phone and nothing
				changed in the car.
			</p>
			<p>
				From there the box forwards onto <strong>the house's wired connection</strong> over
				Ethernet, translating as needed: NAT66 for the private prefix, and DNS64 with NAT64 for
				destinations that have no IPv6 address. It is a route onto an internet connection that
				already exists — the box does not create one. When its upstream health checks fail, or
				the car starts to leave, it withdraws the route so the phone falls back to cellular.
				<span class="tag">source</span>
			</p>
			<p class="more">
				<a href={`${WIKI}/How-It-Works`}>How it works →</a>
				<a href={`${WIKI}/Getting-Started`}>Set it up →</a>
			</p>
		</section>

		<section>
			<h2>Measured limits, and the switch you have to tap</h2>
			<p>
				<strong>Nothing works for your apps until you tap once.</strong> iOS ranks a CarPlay
				network's service as low as it goes, which gets it only a scoped default route:
				background daemons use it, applications ignore it. In Settings, <em>Check For Internet
				Access</em> → <em>Use For Internet</em> lifts that. Before the tap the box can look
				perfect while no user-facing app moves a byte. <span class="tag">measured</span>
			</p>
			<p>
				Afterwards iOS often re-checks by itself — across absences of 15 to 29 minutes the
				phone came back 4 to 24 seconds after the box resumed. <strong>Not reliably</strong>,
				though: two sessions the same morning never validated and forwarded nothing, and a real
				car restart needed the tap again. <span class="tag">measured</span>
			</p>
			<p>
				<strong>The link cannot be driven hard.</strong> Because phone and box are both clients
				of the head unit, every byte is relayed and crosses the air twice, sharing the channel
				with CarPlay's own audio and screen. Pushed hard, the head unit dropped CarPlay
				entirely and needed a hard reset to pair again. Capping the box's egress at 2 Mbit/s
				cut the worst round trip to the phone from <strong>2051 ms to 378 ms</strong> with no
				disconnects. <span class="tag">measured</span>
			</p>
			<p>
				There is spare airtime and it is not usable. CarPlay occupies about 26.5% of the
				channel (median, measured from inside the car), leaving roughly 20 Mbit/s idle — but
				the relay falls over around 3.8 Mbit/s while the air is still free. <strong>The
				bottleneck is the head unit, not the Wi-Fi</strong>, so idle airtime is not an argument
				for a higher cap. <span class="tag">measured</span>
			</p>
			<p class="more">
				<a href={`${WIKI}/Bandwidth-and-Airtime`}>Bandwidth and airtime →</a>
				<a href={`${WIKI}/Troubleshooting`}>Troubleshooting →</a>
			</p>
		</section>

		<section>
			<h2>What this has actually been tested on</h2>
			<p>
				<strong>Two BMWs with factory NBT EVO head units, one iPhone on iOS 27.0.1, one
				garage.</strong> Treat all of it as a result about these head units and this phone, not
				about CarPlay, cars or iOS in general. Other head units announce internet in their
				beacon and behave differently by design.
			</p>
			<p>
				The coverage is uneven even within that. The airtime and shaping figures come from one
				car on single evenings. The full path — phone validated, traffic forwarded — is well
				established on one of the two cars and has been seen on the other only briefly.
			</p>
			<p>
				Still unresolved: whether a phone has ever genuinely used the NAT64 translator (it is
				running and resolving names, but its tunnel has carried almost nothing); both phones
				validated at the same time; and how quickly the phone returns to cellular when driving
				out.
			</p>
			<p class="more">
				<a href={`${WIKI}/Open-Questions`}>Open questions →</a>
				<a href={`${WIKI}/Measuring`}>Reproduce the measurements →</a>
			</p>
		</section>
	</div>

	<footer>
		<a href={REPO}>Source on GitHub</a>
		<span aria-hidden="true">·</span>
		<a href={WIKI}>Project wiki</a>
		<p class="fineprint">
			Not affiliated with or endorsed by BMW or Apple. CarPlay is a trademark of Apple Inc.
		</p>
	</footer>
</main>

<style>
	main {
		max-width: 1180px;
		margin: 0 auto;
		padding: clamp(28px, 5vw, 64px) clamp(18px, 4vw, 40px) 60px;
	}

	.hero {
		max-width: 40ch;
		margin-bottom: clamp(26px, 4vw, 44px);
	}
	.eyebrow {
		margin: 0 0 14px;
		font-size: 0.78rem;
		font-weight: 650;
		letter-spacing: 0.09em;
		text-transform: uppercase;
		color: var(--ink-faint);
	}
	h1 {
		font-size: clamp(1.8rem, 3.4vw, 2.5rem);
		font-weight: 680;
		letter-spacing: -0.03em;
	}
	.lede {
		margin: 0;
		font-size: clamp(1.02rem, 1.5vw, 1.15rem);
		color: var(--ink-soft);
		max-width: 58ch;
	}

	.detail {
		display: grid;
		gap: clamp(30px, 4vw, 44px);
		max-width: 70ch;
	}
	.detail h2 {
		font-size: clamp(1.22rem, 2vw, 1.45rem);
		margin-bottom: 0.5em;
	}
	.detail p {
		color: var(--ink-soft);
		margin: 0 0 0.85em;
	}
	.detail strong {
		color: var(--ink);
		font-weight: 620;
	}

	.more {
		display: flex;
		flex-wrap: wrap;
		gap: 1.2em;
		font-weight: 550;
		font-size: 0.95rem;
		margin-top: 1.1em;
	}
	.more a {
		text-decoration: none;
	}
	.more a:hover {
		text-decoration: underline;
	}

	.tag {
		display: inline-block;
		font-family: var(--mono);
		font-size: 0.68rem;
		font-weight: 600;
		letter-spacing: 0.03em;
		text-transform: uppercase;
		color: var(--ink-faint);
		background: color-mix(in srgb, var(--ink) 8%, transparent);
		border-radius: 4px;
		padding: 0.15em 0.45em;
		vertical-align: 0.1em;
		white-space: nowrap;
	}

	footer {
		border-top: 1px solid var(--rule);
		margin-top: clamp(40px, 5vw, 64px);
		padding-top: 22px;
		font-size: 0.92rem;
		color: var(--ink-faint);
		display: flex;
		align-items: center;
		gap: 0.7em;
		flex-wrap: wrap;
	}
	footer a {
		font-weight: 550;
	}
	.fineprint {
		flex-basis: 100%;
		margin: 6px 0 0;
		font-size: 0.82rem;
	}
</style>
