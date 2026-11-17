<script lang="ts">
	import { Tween } from 'svelte/motion';
	import { cubicInOut } from 'svelte/easing';
	import { draw, fade } from 'svelte/transition';
	import * as P from './physical';
	import type { Scene } from './scenes';

	let { scene }: { scene: Scene } = $props();

	const travel = new Tween(P.S_SURFACE, { duration: 1500, easing: cubicInOut });
	$effect(() => {
		travel.target = scene.underground ? P.S_GARAGE : P.S_SURFACE;
	});

	const pose = $derived(P.poseAt(travel.current));
	const roof = $derived({ x: pose.x + P.CAR.roof.x, y: pose.y + P.CAR.roof.y });
	const ray = $derived(P.skyRay(roof, scene.underground));

	// One marker answers "is there a way out of here", and when the box
	// arrives it does not simply turn green where it is: it travels to where
	// the way out actually is. The concrete overhead has not moved.
	const routed = new Tween(0, { duration: 800, easing: cubicInOut });
	$effect(() => {
		routed.target = scene.box ? 1 : 0;
	});

	const mark = $derived({
		x: roof.x + (P.EXIT.x - roof.x) * routed.current,
		y: P.SLAB_UNDER + (P.EXIT.y - P.SLAB_UNDER) * routed.current
	});
	/** Shown only once the car has actually arrived underneath. */
	const markShown = $derived(!scene.cellular && roof.y > P.SLAB_UNDER);
</script>

<figure class="view">
	<figcaption class="view-title">Where the car is</figcaption>

	<svg
		viewBox="0 0 {P.VIEW.w} {P.VIEW.h}"
		role="img"
		aria-label={scene.underground
			? 'Section through a house. The car is parked in the basement garage, with the structure between it and the cellular tower outside.'
			: 'Section through a house. The car is parked outside at street level, in reach of the cellular tower.'}
	>
		<defs>
			<pattern id="soil-hatch" width="13" height="13" patternUnits="userSpaceOnUse">
				<rect width="13" height="13" fill="var(--soil)" />
				<path d="M -2 4 L 4 -2 M 2 11 L 11 2" stroke="var(--soil-line)" stroke-width="1" />
			</pattern>
			<marker
				id="ray-head"
				viewBox="0 0 10 10"
				refX="8"
				refY="5"
				markerWidth="5"
				markerHeight="5"
				orient="auto-start-reverse"
			>
				<path d="M 0 1 L 9 5 L 0 9 z" fill="var(--internet)" />
			</marker>
		</defs>

		<!-- sky and ground -->
		<rect x="0" y="0" width={P.VIEW.w} height={P.GROUND_Y} fill="var(--sky)" />
		<rect
			x="0"
			y={P.GROUND_Y}
			width={P.VIEW.w}
			height={P.VIEW.h - P.GROUND_Y}
			fill="url(#soil-hatch)"
		/>
		<line
			x1="0"
			y1={P.GROUND_Y}
			x2={P.VIEW.w}
			y2={P.GROUND_Y}
			stroke="var(--concrete-edge)"
			stroke-width="1.5"
		/>

		<!-- house: context only, so it is drawn as thin section linework -->
		<g class="structure">
			<rect x={P.HOUSE.x0} y={P.HOUSE.yTop} width={P.HOUSE.x1 - P.HOUSE.x0} height={P.GROUND_Y - P.HOUSE.yTop} />
			<path
				d="M {P.ROOF.left} {P.ROOF.eave} L {P.ROOF.apex.x} {P.ROOF.apex.y} L {P.ROOF
					.right} {P.ROOF.eave} Z"
			/>
			<rect x="410" y="196" width="54" height="52" />
			<rect x="536" y="196" width="54" height="52" />
		</g>

		<!-- the excavation -->
		<g class="concrete">
			<rect
				x={P.TRENCH_MOUTH_X}
				y={P.GROUND_Y}
				width={664 - P.TRENCH_MOUTH_X}
				height={P.SLAB_UNDER - P.GROUND_Y}
			/>
			<rect
				x={P.BACK_WALL.x0}
				y={P.GROUND_Y}
				width={P.BACK_WALL.x1 - P.BACK_WALL.x0}
				height={P.FLOOR_BOTTOM - P.GROUND_Y}
			/>
			<rect
				x={P.TRENCH_MOUTH_X}
				y={P.FLOOR_Y}
				width={P.BACK_WALL.x0 - P.TRENCH_MOUTH_X}
				height={P.FLOOR_BOTTOM - P.FLOOR_Y}
			/>
			<rect x="132" y={P.GROUND_Y} width="18" height="74" />
		</g>
		<g transform="translate(0,11)">
			<path d={P.RAMP_D} fill="none" stroke="var(--concrete)" stroke-width="22" />
		</g>

		<path d={P.VOID_D} fill="var(--garage)" />
		<path d={P.RAMP_D} fill="none" class="edge" />
		<line x1={P.TRENCH_MOUTH_X} y1={P.FLOOR_Y} x2={P.BACK_WALL.x0} y2={P.FLOOR_Y} class="edge" />
		<line
			x1={P.TRENCH_MOUTH_X}
			y1={P.SLAB_UNDER}
			x2={P.BACK_WALL.x0}
			y2={P.SLAB_UNDER}
			class="edge"
		/>

		<!-- the tower keeps transmitting in every state; what changes is reach -->
		<g class="tower">
			<path d="M 700 {P.GROUND_Y} L 709 {P.TOWER.tip} M 724 {P.GROUND_Y} L 715 {P.TOWER.tip}" />
			<path d="M 703 252 L 721 252 M 706 204 L 718 204 M 708 162 L 716 162" />
			<path d="M 700 {P.GROUND_Y} L 718 204 M 724 {P.GROUND_Y} L 706 204" class="hair" />
			<path d="M 701 134 L 723 134 M 704 146 L 720 146" class="antenna" />
		</g>
		<g class="fan">
			<path d={P.fanArc(34)} style="animation-delay: 0s" />
			<path d={P.fanArc(52)} style="animation-delay: 0.3s" />
			<path d={P.fanArc(70)} style="animation-delay: 0.6s" />
		</g>

		{#if scene.box}
			<!-- the box, and the wired link it forwards onto -->
			<g class="box-group" in:fade={{ duration: 300, delay: 420 }} out:fade={{ duration: 160 }}>
				<rect x={P.BOX.x} y={P.BOX.y} width={P.BOX.w} height={P.BOX.h} rx="4" class="box" />
				<circle cx={P.BOX.x + 10} cy={P.BOX.y + 9} r="2.6" class="led" />
				<path
					d="M {P.BOX.x + 19} {P.BOX.y + 19} h 26 M {P.BOX.x + 19} {P.BOX.y + 24} h 17"
					class="vent"
				/>
			</g>
		{/if}

		<!-- the car: the subject of the picture -->
		<g transform="translate({pose.x.toFixed(2)},{pose.y.toFixed(2)})">
			<g transform="rotate({pose.angle.toFixed(2)}) scale({P.CAR_SCALE})">
				<ellipse cx="0" cy="-2" rx="68" ry="5" class="shadow" />
				<path d={P.CAR.body} class="car-body" />
				<path d={P.CAR.sideGlass} class="glass" />
				<path d={P.CAR.windscreen} class="glass" />
				<!-- the phone and the head unit really are in here; the inset is
				     the same two devices, drawn big enough to label -->
				<rect x="-4" y="-57" width="8" height="14" rx="1.6" class="in-car-phone" />
				<rect x="40" y="-45" width="13" height="8" rx="1.4" class="in-car-hu" />
				<g class="wheels">
					{#each P.CAR.wheels as w (w.x)}
						<circle cx={w.x} cy={w.y} r={P.CAR.r} />
						<circle cx={w.x} cy={w.y} r="7" class="hub" />
					{/each}
				</g>
				<rect
					x={P.CAR.callout.x}
					y={P.CAR.callout.y}
					width={P.CAR.callout.w}
					height={P.CAR.callout.h}
					rx="5"
					class="callout"
				/>
				<circle cx={P.CAR.callout.x} cy={P.CAR.callout.y} r="11" class="callout-badge" />
				<text x={P.CAR.callout.x} y={P.CAR.callout.y + 4} class="callout-num">1</text>
			</g>
		</g>

		<!-- can the phone reach the tower from where it is? -->
		{#if ray.show}
			<g class="ray" class:blocked={!scene.cellular} in:fade={{ duration: 260 }}>
				<line x1={roof.x} y1={roof.y} x2={ray.to.x} y2={ray.to.y} />
			</g>
		{/if}

		{#if scene.box}
			<!-- the route the box opens: out of the car, through the box, up the
			     cable into the house. Drawn so the marker has somewhere to go. -->
			<g class="route">
				<path
					class="hop"
					d="M {roof.x} {roof.y} L {P.BOX.x} {P.BOX.y + P.BOX.h / 2}"
					in:draw={{ duration: 380, delay: 120 }}
				/>
				<path
					class="riser"
					d="M {P.EXIT.x} {P.BOX.y} L {P.EXIT.x} {P.EXIT.y}"
					in:draw={{ duration: 480, delay: 320 }}
				/>
			</g>
		{/if}

		{#if markShown}
			<!-- red cross at the concrete, green tick at the cable; the same
			     marker, travelling between them -->
			<g class="mark" class:open={scene.box} in:fade={{ duration: 240 }}>
				<circle cx={mark.x} cy={mark.y} r="11" />
				{#if routed.current < 0.5}
					<path
						d="M {mark.x - 4.5} {mark.y - 4.5} l 9 9 M {mark.x + 4.5} {mark.y - 4.5} l -9 9"
					/>
				{:else}
					<path d="M {mark.x - 5} {mark.y} l 3.6 4.2 L {mark.x + 5.2} {mark.y - 5}" />
				{/if}
				{#if routed.current < 0.12}
					<text x={mark.x + 17} y={mark.y - 8}>no path out</text>
				{:else if routed.current > 0.88}
					<text x={mark.x + 17} y={mark.y - 8} in:fade={{ duration: 280 }}>path out</text>
				{/if}
			</g>
		{/if}

		<!-- labels: few, and large enough to read -->
		<g class="label">
			<text class="muted" x="12" y="322">street level</text>
			<text class="mid" x="688" y="100">cell tower</text>
			{#if scene.box}
				<g in:fade={{ duration: 300, delay: 480 }}>
					<text class="mid" x={P.BOX.x + P.BOX.w / 2} y={P.BOX.y + P.BOX.h + 20}>
						Linux box
					</text>
				</g>
			{/if}
			{#if ray.show && scene.cellular}
				<text class="internet-text" x={ray.to.x + 10} y={ray.to.y - 6}>cellular</text>
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

	svg {
		display: block;
		width: 100%;
		height: auto;
		border: 1px solid var(--rule);
		border-radius: 10px;
		background: var(--paper-raised);
	}

	.structure rect,
	.structure path {
		fill: var(--wall);
		stroke: var(--concrete-edge);
		stroke-width: 1.4;
	}
	.concrete rect {
		fill: var(--concrete);
	}
	.edge {
		fill: none;
		stroke: var(--concrete-edge);
		stroke-width: 1.6;
	}

	.tower path {
		fill: none;
		stroke: var(--ink-faint);
		stroke-width: 2.4;
	}
	.tower .hair {
		stroke-width: 1;
		stroke-opacity: 0.55;
	}
	.tower .antenna {
		stroke: var(--internet);
		stroke-width: 3.4;
	}
	.fan path {
		fill: none;
		stroke: var(--internet);
		stroke-width: 2.6;
		stroke-linecap: round;
		opacity: 0.5;
		animation: fan 2s ease-out infinite;
	}
	@keyframes fan {
		0% {
			opacity: 0.22;
		}
		35% {
			opacity: 0.95;
		}
		100% {
			opacity: 0.22;
		}
	}

	.shadow {
		fill: rgba(0, 0, 0, 0.16);
	}
	.car-body {
		fill: var(--ink-soft);
		stroke: var(--ink);
		stroke-width: 1.4;
	}
	.glass {
		fill: var(--sky);
	}
	.in-car-phone {
		fill: var(--carplay);
	}
	.in-car-hu {
		fill: var(--carplay);
		opacity: 0.75;
	}
	.wheels circle {
		fill: #14171c;
	}
	.wheels .hub {
		fill: var(--concrete);
	}
	.callout {
		fill: none;
		stroke: var(--carplay);
		stroke-width: 1.6;
		stroke-dasharray: 4 3;
	}
	.callout-badge {
		fill: var(--carplay);
	}
	.callout-num {
		fill: #fff;
		font-family: var(--font);
		font-size: 13px;
		font-weight: 700;
		text-anchor: middle;
	}

	.ray line {
		stroke: var(--internet);
		stroke-width: 3;
		stroke-linecap: round;
		marker-end: url(#ray-head);
	}
	.ray.blocked line {
		stroke: var(--unavail);
		stroke-width: 2.6;
		stroke-dasharray: 5 4;
		marker-end: none;
	}
	.mark circle {
		fill: var(--paper-raised);
		stroke: var(--fail);
		stroke-width: 2.4;
		transition: stroke 0.5s ease;
	}
	.mark path {
		stroke: var(--fail);
		stroke-width: 2.6;
		stroke-linecap: round;
		stroke-linejoin: round;
		fill: none;
		transition: stroke 0.5s ease;
	}
	.mark text {
		font-family: var(--font);
		font-size: 15px;
		font-weight: 650;
		fill: var(--fail);
		transition: fill 0.5s ease;
	}
	.mark.open circle,
	.mark.open path {
		stroke: var(--ok);
	}
	.mark.open text {
		fill: var(--ok);
	}

	.route path {
		fill: none;
		stroke: var(--ok);
		stroke-width: 3.4;
		stroke-linecap: round;
	}

	.label text {
		font-family: var(--font);
		font-size: 15px;
		font-weight: 550;
		fill: var(--ink);
	}
	.label text.mid {
		text-anchor: middle;
	}
	.label text.muted {
		font-size: 14px;
		font-weight: 450;
		fill: var(--ink-faint);
	}
	.label .internet-text {
		fill: var(--internet);
		font-weight: 600;
	}

	/* Stacked, each view is about half the width it had, so the type grows
	   inside the viewBox rather than shrinking with the drawing. */
	@media (max-width: 640px) {
		.label text {
			font-size: 23px;
		}
		.label text.muted {
			font-size: 21px;
		}
	}

	.box {
		fill: var(--ink-soft);
		stroke: var(--ink);
		stroke-width: 1.3;
	}
	.led {
		fill: #8ce0a5;
	}
	.vent {
		stroke: var(--paper-raised);
		stroke-opacity: 0.75;
		stroke-width: 1.8;
		fill: none;
	}

</style>
