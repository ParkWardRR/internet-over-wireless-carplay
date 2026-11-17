<script lang="ts">
	import CutawayScene from '$lib/scene/CutawayScene.svelte';

	const REPO = 'https://github.com/ParkWardRR/internet-over-wireless-carplay';
	const WIKI = `${REPO}/wiki`;

	const gotchas = [
		{
			n: '1',
			head: 'You have to tap a switch on the phone',
			body:
				'iOS gives a CarPlay network the lowest service rank it has: background daemons will ' +
				'use it, your apps will not. Until you tap Check For Internet Access → Use For ' +
				'Internet, Maps fails while everything on the box looks perfect. On one instrumented ' +
				'drive, 1.36 MB moved through the box and no user-facing app touched a byte.',
			label: 'measured',
			href: `${WIKI}/Troubleshooting`
		},
		{
			n: '2',
			head: 'Do not saturate it',
			body:
				'Every byte is relayed by the head unit, so it crosses the air twice and competes ' +
				'with CarPlay’s own audio and screen. Pushed hard, the head unit dropped CarPlay ' +
				'entirely and needed a hard reset to pair again. The box caps itself at 2 Mbit/s for ' +
				'that reason.',
			label: 'measured',
			href: `${WIKI}/Bandwidth-and-Airtime`
		},
		{
			n: '3',
			head: 'There is spare airtime and you cannot use it',
			body:
				'CarPlay occupies about a quarter of the channel, leaving roughly 20 Mbit/s idle — ' +
				'but the head unit’s relay collapses at around 3.8 Mbit/s. The bottleneck is the ' +
				'relay, not the air, so free airtime is not evidence for a higher cap.',
			label: 'measured',
			href: `${WIKI}/Bandwidth-and-Airtime`
		}
	];
</script>

<svelte:head>
	<title>Internet over wireless CarPlay</title>
</svelte:head>

<main>
	<header class="hero">
		<p class="eyebrow">BMW F32 · NBT EVO · iOS 27</p>
		<h1>Wireless CarPlay is the connection to your dashboard — not your internet connection.</h1>
		<p class="lede">
			Underground, CarPlay can stay perfectly connected while cellular service disappears. Two
			independent links, only one of which failed. This project puts a small Linux box on the
			car's own network and hands the phone a way out.
		</p>
	</header>

	<section class="stage" aria-labelledby="diagram-heading">
		<h2 id="diagram-heading" class="sr-only">The two connections, above ground and below</h2>
		<CutawayScene />
	</section>

	<section class="prose">
		<h2>What the car's network actually is</h2>
		<p>
			Your car's wireless CarPlay creates its own private Wi-Fi network so the phone can talk to
			the dashboard. That network carries <strong>no internet</strong> — it is a cable
			replacement, not a hotspot. Normally you never notice, because the phone uses cellular for
			everything else. Park underground and that stops.
		</p>
		<p>
			The fix does not fight the car for IPv4. The head unit is the DHCP server, it offers no
			router and no DNS, and iOS hides manual IP settings on a CarPlay network. But the link has
			<em>no IPv6 router at all</em> — so the box becomes the only one, advertises a private
			prefix and a DNS server, and the phone configures itself with plain SLAAC. The car is not
			modified. The phone's IP settings are not changed.
		</p>
		<p class="more">
			<a href={`${WIKI}/How-It-Works`}>How it works →</a>
			<a href={`${WIKI}/The-CarPlay-Network`}>What the network is →</a>
			<a href={`${WIKI}/Getting-Started`}>Set it up →</a>
		</p>
	</section>

	<section class="prose">
		<h2>The three things that catch everyone out</h2>
		<ol class="gotchas">
			{#each gotchas as g (g.n)}
				<li>
					<span class="num">{g.n}</span>
					<div>
						<h3>{g.head}</h3>
						<p>{g.body}</p>
						<a href={g.href}><span class="tag">{g.label}</span> read the detail →</a>
					</div>
				</li>
			{/each}
		</ol>
	</section>

	<section class="prose">
		<h2>How confident is any of this?</h2>
		<p>
			Every claim in the wiki is labelled <span class="tag">measured</span>,
			<span class="tag">source</span>, <span class="tag">emulator</span> or
			<span class="tag">inferred</span>. Measured on two BMWs with factory NBT EVO head units and
			one iPhone on iOS 27, in one garage. Treat it as a solid data point, not a universal law —
			including the conclusions that turned out to be wrong, which are kept in
			<code>docs/test-log.md</code>.
		</p>
		<p class="more">
			<a href={`${WIKI}/Open-Questions`}>What is still unproven →</a>
			<a href={`${WIKI}/Measuring`}>Measure it yourself →</a>
		</p>
	</section>

	<footer>
		<a href={REPO}>Source on GitHub</a>
		<span aria-hidden="true">·</span>
		<a href={WIKI}>Project wiki</a>
		<p class="fineprint">
			Nothing here is affiliated with or endorsed by BMW or Apple. CarPlay is a trademark of
			Apple Inc.
		</p>
	</footer>
</main>

<style>
	main {
		max-width: 1180px;
		margin: 0 auto;
		padding: clamp(28px, 6vw, 72px) clamp(18px, 4vw, 40px) 60px;
	}

	.sr-only {
		position: absolute;
		width: 1px;
		height: 1px;
		padding: 0;
		margin: -1px;
		overflow: hidden;
		clip-path: inset(50%);
		white-space: nowrap;
	}

	.hero {
		max-width: 52ch;
		margin-bottom: clamp(28px, 5vw, 48px);
	}
	.eyebrow {
		margin: 0 0 14px;
		font-size: 0.78rem;
		font-weight: 650;
		letter-spacing: 0.1em;
		text-transform: uppercase;
		color: var(--ink-faint);
	}
	h1 {
		font-size: clamp(1.8rem, 3.6vw, 2.55rem);
		font-weight: 680;
		letter-spacing: -0.03em;
	}
	.lede {
		margin: 0;
		font-size: clamp(1.02rem, 1.6vw, 1.18rem);
		color: var(--ink-soft);
		max-width: 56ch;
	}

	.stage {
		margin-bottom: clamp(40px, 7vw, 76px);
	}

	.prose {
		max-width: 68ch;
		margin-bottom: clamp(36px, 6vw, 64px);
	}
	.prose h2 {
		font-size: clamp(1.3rem, 2.4vw, 1.6rem);
		margin-bottom: 0.6em;
	}
	.prose p {
		color: var(--ink-soft);
	}
	.more {
		display: flex;
		flex-wrap: wrap;
		gap: 1.2em;
		font-weight: 550;
		font-size: 0.95rem;
	}
	.more a {
		text-decoration: none;
	}
	.more a:hover {
		text-decoration: underline;
	}

	.gotchas {
		list-style: none;
		padding: 0;
		margin: 0;
		display: grid;
		gap: 22px;
	}
	.gotchas li {
		display: flex;
		gap: 16px;
		padding-top: 20px;
		border-top: 1px solid var(--rule);
	}
	.num {
		flex: 0 0 auto;
		width: 30px;
		height: 30px;
		border-radius: 50%;
		display: grid;
		place-items: center;
		background: color-mix(in srgb, var(--carplay) 15%, transparent);
		color: var(--carplay);
		font-size: 0.9rem;
		font-weight: 700;
	}
	.gotchas h3 {
		font-size: 1.06rem;
		margin-bottom: 0.3em;
	}
	.gotchas p {
		margin: 0 0 0.6em;
	}
	.gotchas a {
		font-size: 0.9rem;
		font-weight: 550;
		text-decoration: none;
	}
	.gotchas a:hover {
		text-decoration: underline;
	}

	.tag {
		display: inline-block;
		font-family: var(--mono);
		font-size: 0.72rem;
		font-weight: 600;
		letter-spacing: 0.02em;
		text-transform: uppercase;
		color: var(--ink-soft);
		background: color-mix(in srgb, var(--ink) 9%, transparent);
		border-radius: 4px;
		padding: 0.15em 0.45em;
		margin-right: 0.3em;
	}

	footer {
		border-top: 1px solid var(--rule);
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
