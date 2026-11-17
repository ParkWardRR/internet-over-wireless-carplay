<script lang="ts">
	import { fade } from 'svelte/transition';
	import type { Scene } from './scenes';

	let { scene }: { scene: Scene } = $props();

	const VIEW = { w: 540, h: 656 };

	// Fixed positions. Nothing moves between states; things appear, and the
	// flows along them change. That is what makes the three states comparable.
	//
	// The house is here in every state, with its own Wi-Fi and its own line to
	// the internet, because the question the picture has to answer is why the
	// phone cannot simply use them.
	const N = {
		tower: { x: 72, y: 36, w: 150, h: 56, title: 'Cell tower', sub: 'mobile network' },
		phone: { x: 72, y: 186, w: 150, h: 56, title: 'iPhone', sub: 'one Wi-Fi radio' },
		hu: { x: 346, y: 186, w: 150, h: 56, title: 'Head unit', sub: 'access point' },
		wifi: { x: 72, y: 358, w: 150, h: 56, title: 'Home Wi-Fi', sub: 'the house network' },
		box: { x: 346, y: 358, w: 150, h: 56, title: 'Linux box', sub: 'a second radio' },
		router: { x: 346, y: 470, w: 150, h: 56, title: 'Home router', sub: 'the house line' },
		net: { x: 346, y: 580, w: 150, h: 56, title: 'Internet', sub: '' }
	};

	const CAR_GROUP = { x: 56, y: 150, w: 460, h: 112 };
	const HOUSE_GROUP = { x: 56, y: 322, w: 460, h: 226 };

	// Link spines, and the parallel offsets the traffic flows ride on.
	const CELL = { x: 147, y1: 92, y2: 186 };
	const WIFI_CAR = { y: 214, x1: 222, x2: 346 };
	const HOME = { x: 147, y1: 242, y2: 358 };
	const WIFI_BOX = { x: 421, y1: 242, y2: 358 };
	const LAN = 'M 222 386 L 284 386 L 284 498 L 346 498';
	const ETH = { x: 421, y1: 414, y2: 470 };
	const ISP = { x: 421, y1: 526, y2: 580 };
	const OFF = 9;

	const CELL_MARK = 122;
	const HOME_MARK = 300;
</script>

{#snippet node(n: { x: number; y: number; w: number; h: number; title: string; sub: string }, tone: string)}
	<g class="node {tone}">
		<rect x={n.x} y={n.y} width={n.w} height={n.h} rx="7" />
		<text class="n-title" x={n.x + 14} y={n.sub ? n.y + 25 : n.y + 34}>{n.title}</text>
		{#if n.sub}
			<text class="n-sub" x={n.x + 14} y={n.y + 43}>{n.sub}</text>
		{/if}
	</g>
{/snippet}

<figure class="view">
	<figcaption class="view-title">
		<span class="badge">1</span> What is connected to what
	</figcaption>

	<svg
		viewBox="0 0 {VIEW.w} {VIEW.h}"
		role="img"
		aria-label={`Network diagram. ${scene.title}. ${scene.body.replace(/\n/g, ' ')}`}
	>
		<defs>
			<marker
				id="a-carplay"
				viewBox="0 0 10 10"
				refX="7"
				refY="5"
				markerWidth="4.6"
				markerHeight="4.6"
				orient="auto-start-reverse"
			>
				<path d="M 0 1 L 9 5 L 0 9 z" fill="var(--carplay)" />
			</marker>
			<marker
				id="a-internet"
				viewBox="0 0 10 10"
				refX="7"
				refY="5"
				markerWidth="4.6"
				markerHeight="4.6"
				orient="auto-start-reverse"
			>
				<path d="M 0 1 L 9 5 L 0 9 z" fill="var(--internet)" />
			</marker>
		</defs>

		<!-- grouping mirrors the physical view -->
		<g class="group">
			<rect x={CAR_GROUP.x} y={CAR_GROUP.y} width={CAR_GROUP.w} height={CAR_GROUP.h} rx="10" />
			<text class="end" x={CAR_GROUP.x + CAR_GROUP.w - 16} y={CAR_GROUP.y + 24}>IN THE CAR</text>
		</g>
		<g class="group">
			<rect
				x={HOUSE_GROUP.x}
				y={HOUSE_GROUP.y}
				width={HOUSE_GROUP.w}
				height={HOUSE_GROUP.h}
				rx="10"
			/>
			<text class="mid" x={HOUSE_GROUP.x + HOUSE_GROUP.w / 2} y={HOUSE_GROUP.y + 24}>
				IN THE HOUSE
			</text>
		</g>

		<!-- physical links: solid, neutral. These exist regardless of traffic. -->
		<g class="phys">
			<line x1={CELL.x} y1={CELL.y1} x2={CELL.x} y2={CELL.y2} class:dead={!scene.cellular} />
			<line x1={WIFI_CAR.x1} y1={WIFI_CAR.y} x2={WIFI_CAR.x2} y2={WIFI_CAR.y} />
			<!-- the link the phone cannot have: its radio is already spoken for -->
			<line x1={HOME.x} y1={HOME.y1} x2={HOME.x} y2={HOME.y2} class="dead" />
			<path d={LAN} class="wired" fill="none" />
			<line x1={ISP.x} y1={ISP.y1} x2={ISP.x} y2={ISP.y2} class="wired" />
			{#if scene.box}
				<g in:fade={{ duration: 280, delay: 360 }} out:fade={{ duration: 140 }}>
					<line x1={WIFI_BOX.x} y1={WIFI_BOX.y1} x2={WIFI_BOX.x} y2={WIFI_BOX.y2} />
					<line x1={ETH.x} y1={ETH.y1} x2={ETH.x} y2={ETH.y2} class="wired" />
				</g>
			{/if}
		</g>

		<!-- traffic flows: dashed, coloured, arrowed at both ends -->
		<g class="flows">
			<!-- CarPlay never changes. That is the point of all three states. -->
			<line
				class="flow carplay"
				x1={WIFI_CAR.x1 + 4}
				y1={WIFI_CAR.y - OFF}
				x2={WIFI_CAR.x2 - 4}
				y2={WIFI_CAR.y - OFF}
			/>

			{#if scene.cellular}
				<line
					class="flow internet"
					x1={CELL.x + OFF}
					y1={CELL.y1 + 4}
					x2={CELL.x + OFF}
					y2={CELL.y2 - 4}
				/>
			{/if}

			{#if scene.box}
				<g in:fade={{ duration: 300, delay: 460 }} out:fade={{ duration: 140 }}>
					<line
						class="flow internet"
						x1={WIFI_CAR.x1 + 4}
						y1={WIFI_CAR.y + OFF}
						x2={WIFI_CAR.x2 - 4}
						y2={WIFI_CAR.y + OFF}
					/>
					<line
						class="flow internet"
						x1={WIFI_BOX.x + OFF}
						y1={WIFI_BOX.y1 + 4}
						x2={WIFI_BOX.x + OFF}
						y2={WIFI_BOX.y2 - 4}
					/>
					<line
						class="flow internet"
						x1={ETH.x + OFF}
						y1={ETH.y1 + 4}
						x2={ETH.x + OFF}
						y2={ETH.y2 - 4}
					/>
					<line
						class="flow internet"
						x1={ISP.x + OFF}
						y1={ISP.y1 + 4}
						x2={ISP.x + OFF}
						y2={ISP.y2 - 4}
					/>
				</g>
			{/if}
		</g>

		<!-- what is not available, and why -->
		{#if !scene.cellular}
			<g class="stop" in:fade={{ duration: 240, delay: 400 }}>
				<circle cx={CELL.x} cy={CELL_MARK} r="12" />
				<path
					d="M {CELL.x - 5} {CELL_MARK - 5} l 10 10 M {CELL.x + 5} {CELL_MARK - 5} l -10 10"
				/>
			</g>
		{/if}
		<g class="stop">
			<circle cx={HOME.x} cy={HOME_MARK} r="12" />
			<path
				d="M {HOME.x - 5} {HOME_MARK - 5} l 10 10 M {HOME.x + 5} {HOME_MARK - 5} l -10 10"
			/>
		</g>

		<!-- nodes -->
		{@render node(N.tower, scene.cellular ? 'live' : 'dim')}
		{@render node(N.phone, 'live')}
		{@render node(N.hu, 'live')}
		{@render node(N.wifi, 'live')}
		{@render node(N.router, 'live')}
		{@render node(N.net, 'live')}
		{#if scene.box}
			<g in:fade={{ duration: 300, delay: 420 }} out:fade={{ duration: 140 }}>
				{@render node(N.box, 'live')}
			</g>
		{/if}

		<!-- link labels -->
		<g class="link-label">
			<text class="start" x={CELL.x + 18} y={CELL_MARK - 4}>cellular</text>
			{#if !scene.cellular}
				<text class="start fail" x={CELL.x + 18} y={CELL_MARK + 15}>no signal here</text>
			{/if}

			<text class="mid" x={(WIFI_CAR.x1 + WIFI_CAR.x2) / 2} y={WIFI_CAR.y + 34}>
				Wi-Fi · the car network
			</text>

			<!-- the answer to "why not just use the house Wi-Fi" -->
			<text class="mid fail" x={N.wifi.x + N.wifi.w / 2} y={N.wifi.y + 84}>
				one radio, one network
			</text>
			<text class="mid muted" x={N.wifi.x + N.wifi.w / 2} y={N.wifi.y + 102}>
				and it is on the car’s
			</text>

			{#if scene.box}
				<g in:fade={{ duration: 300, delay: 460 }}>
					<text class="end" x={WIFI_BOX.x - 14} y="284">same Wi-Fi,</text>
					<text class="end" x={WIFI_BOX.x - 14} y="302">relayed by the AP</text>
					<text class="end muted" x={WIFI_BOX.x - 14} y="320">so it crosses the air twice</text>
					<text class="start" x={ETH.x + 20} y={ETH.y1 + 34}>Ethernet</text>
				</g>
			{/if}
		</g>
	</svg>
</figure>

<style>
	.view {
		margin: 0;
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
	.view-title {
		display: flex;
		align-items: center;
		gap: 8px;
		font-size: 0.82rem;
		font-weight: 600;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--ink-faint);
		margin-bottom: 8px;
	}
	.badge {
		display: grid;
		place-items: center;
		width: 19px;
		height: 19px;
		border-radius: 50%;
		background: var(--carplay);
		color: #fff;
		font-size: 0.68rem;
		font-weight: 700;
		letter-spacing: 0;
	}

	svg {
		display: block;
		width: 100%;
		height: auto;
		border: 1px solid var(--rule);
		border-radius: 10px;
		background: var(--paper-raised);
	}

	.group rect {
		fill: var(--panel);
		stroke: var(--rule);
		stroke-width: 1.4;
		stroke-dasharray: 6 4;
	}
	.group text {
		font-family: var(--font);
		font-size: 12.5px;
		font-weight: 650;
		letter-spacing: 0.08em;
		fill: var(--ink-faint);
	}
	.group text.end {
		text-anchor: end;
	}
	.group text.mid {
		text-anchor: middle;
	}

	.node rect {
		fill: var(--paper-raised);
		stroke: var(--link);
		stroke-width: 1.8;
	}
	.node.dim rect {
		stroke: var(--unavail);
		stroke-dasharray: 5 4;
	}
	.node .n-title {
		font-family: var(--font);
		font-size: 18px;
		font-weight: 650;
		fill: var(--ink);
	}
	.node .n-sub {
		font-family: var(--font);
		font-size: 14.5px;
		fill: var(--ink-faint);
	}
	.node.dim .n-title,
	.node.dim .n-sub {
		fill: var(--ink-faint);
	}

	.phys line,
	.phys path {
		stroke: var(--link);
		stroke-width: 3;
	}
	.phys .wired {
		stroke-width: 4.5;
		stroke-linecap: round;
		stroke-linejoin: round;
	}
	.phys .dead {
		stroke: var(--unavail);
		stroke-dasharray: 5 5;
		stroke-width: 2.4;
	}

	.flow {
		stroke-width: 3.4;
		stroke-linecap: butt;
		stroke-dasharray: 9 11;
		animation: flow 0.95s linear infinite;
	}
	.flow.carplay {
		stroke: var(--carplay);
		marker-start: url(#a-carplay);
		marker-end: url(#a-carplay);
	}
	.flow.internet {
		stroke: var(--internet);
		marker-start: url(#a-internet);
		marker-end: url(#a-internet);
	}
	@keyframes flow {
		to {
			stroke-dashoffset: -20;
		}
	}

	.stop circle {
		fill: var(--paper-raised);
		stroke: var(--fail);
		stroke-width: 2.4;
	}
	.stop path {
		stroke: var(--fail);
		stroke-width: 2.6;
		stroke-linecap: round;
		fill: none;
	}

	.link-label text {
		font-family: var(--font);
		font-size: 15px;
		font-weight: 550;
		fill: var(--ink-soft);
	}
	.link-label text.mid {
		text-anchor: middle;
	}
	.link-label text.end {
		text-anchor: end;
	}
	.link-label text.start {
		text-anchor: start;
	}
	.link-label text.muted {
		font-size: 13.5px;
		font-weight: 450;
		fill: var(--ink-faint);
	}
	.link-label text.fail {
		fill: var(--fail);
		font-weight: 600;
	}

	/* Stacked on a narrow screen, this drawing is about half as wide, so the
	   type grows inside the viewBox rather than shrinking with it. */
	@media (max-width: 640px) {
		.node .n-title {
			font-size: 21px;
		}
		.node .n-sub {
			font-size: 17px;
		}
		.link-label text {
			font-size: 17.5px;
		}
		.link-label text.muted {
			font-size: 16px;
		}
		.group text {
			font-size: 15px;
		}
	}
</style>
