<script lang="ts">
	import { Tween } from 'svelte/motion';
	import { cubicInOut } from 'svelte/easing';
	import { fade } from 'svelte/transition';
	import * as G from './geometry';
	import { SCENES, DEFAULT_SCENE, type SceneId } from './scenes';

	let activeId = $state<SceneId>(DEFAULT_SCENE);
	const scene = $derived(SCENES.find((s) => s.id === activeId) ?? SCENES[0]);

	// The car drives; everything else holds still. One tween, one source of
	// truth for where the car is, and the links hang off it.
	const travel = new Tween(G.S_SURFACE, { duration: 1700, easing: cubicInOut });
	$effect(() => {
		travel.target = scene.underground ? G.S_GARAGE : G.S_SURFACE;
	});

	const pose = $derived(G.poseAt(travel.current));
	const phoneTop = $derived({ x: pose.x + G.PHONE_TOP.x, y: pose.y + G.PHONE_TOP.y });
	const headUnit = $derived({ x: pose.x + G.HEAD_UNIT.x, y: pose.y + G.HEAD_UNIT.y });

	const cellD = $derived(G.cellularPath(phoneTop));
	const blockedAt = $derived(scene.underground ? G.cellularBlockedAt(phoneTop) : null);

	const boxHopD = $derived(
		`M ${headUnit.x.toFixed(1)} ${headUnit.y.toFixed(1)}` +
			` Q ${(headUnit.x + 72).toFixed(1)} ${(headUnit.y - 40).toFixed(1)}` +
			` ${G.BOX.x} ${G.BOX.y + G.BOX.h / 2}`
	);

	// Phone to head unit, in the car's own coordinates — it never has to be
	// recomputed, because both ends travel together.
	const carplayD =
		`M ${G.PHONE_SIDE.x} ${G.PHONE_SIDE.y}` +
		` Q ${G.PHONE_SIDE.x - 46} ${G.PHONE_SIDE.y + 4}` +
		` ${G.HEAD_UNIT.x} ${G.HEAD_UNIT.y}`;
</script>

<figure class="scene">
	<div
		class="tabs"
		role="radiogroup"
		aria-label="Where the car is parked"
	>
		{#each SCENES as s (s.id)}
			<button
				type="button"
				role="radio"
				aria-checked={s.id === activeId}
				class:active={s.id === activeId}
				onclick={() => (activeId = s.id)}
			>
				{s.tab}
			</button>
		{/each}
	</div>

	<div class="frame">
		<svg
			viewBox="0 0 {G.VIEW.w} {G.VIEW.h}"
			role="img"
			aria-label={`Cutaway of a house with a basement garage. ${scene.title}. ${scene.caption}`}
		>
			<defs>
				<pattern id="soil" width="14" height="14" patternUnits="userSpaceOnUse">
					<rect width="14" height="14" fill="var(--soil)" />
					<path
						d="M -2 4 L 4 -2 M 2 12 L 12 2 M 10 16 L 16 10"
						stroke="var(--concrete-dark)"
						stroke-opacity="0.35"
						stroke-width="1"
						fill="none"
					/>
				</pattern>

				<linearGradient id="void" x1="0" y1="0" x2="0" y2="1">
					<stop offset="0%" stop-color="var(--garage)" />
					<stop offset="100%" stop-color="var(--garage)" stop-opacity="0.82" />
				</linearGradient>

				<linearGradient id="skyfill" x1="0" y1="0" x2="0" y2="1">
					<stop offset="0%" stop-color="var(--sky)" />
					<stop offset="100%" stop-color="var(--paper)" />
				</linearGradient>

				<filter id="lit" x="-60%" y="-60%" width="220%" height="220%">
					<feGaussianBlur stdDeviation="4" result="b" />
					<feMerge>
						<feMergeNode in="b" />
						<feMergeNode in="SourceGraphic" />
					</feMerge>
				</filter>

				<clipPath id="voidClip">
					<path d={G.VOID_D} />
				</clipPath>
			</defs>

			<!-- sky -->
			<rect x="0" y="0" width={G.VIEW.w} height={G.GROUND_Y} fill="url(#skyfill)" />

			<!-- The cellular arc is drawn here, behind the building, so concrete and
			     soil occlude it for free: underground you watch it vanish into the slab. -->
			<g class="cell" class:dead={scene.cellular.dead}>
				<path class="cell-halo" d={cellD} />
				<path class="cell-line" d={cellD} />
			</g>

			<!-- tower -->
			<g class="tower">
				<path
					d="M 1062 {G.GROUND_Y} L 1077 {G.TOWER.tip + 12} M 1098 {G.GROUND_Y} L 1083 {G.TOWER
						.tip + 12}"
				/>
				<path
					d="M 1066 248 L 1094 248 M 1069 204 L 1091 204 M 1072 160 L 1088 160 M 1074 120 L 1086 120"
				/>
				<path d="M 1062 {G.GROUND_Y} L 1091 204 M 1098 {G.GROUND_Y} L 1069 204" class="brace" />
				<path d="M 1077 {G.TOWER.tip + 12} L 1083 {G.TOWER.tip + 12}" />
				<path d="M 1064 92 L 1096 92 M 1068 104 L 1092 104" class="antenna" />
			</g>
			<g class="fan" class:dead={scene.cellular.dead}>
				<path d={G.fanArc(30)} style="animation-delay: 0s" />
				<path d={G.fanArc(46)} style="animation-delay: 0.25s" />
				<path d={G.fanArc(62)} style="animation-delay: 0.5s" />
			</g>

			<!-- ground, soil, and the structure cut through it -->
			<rect
				x="0"
				y={G.GROUND_Y}
				width={G.VIEW.w}
				height={G.VIEW.h - G.GROUND_Y}
				fill="url(#soil)"
			/>
			<line x1="0" y1={G.GROUND_Y} x2={G.VIEW.w} y2={G.GROUND_Y} class="grade" />

			<!-- driveway -->
			<rect x="0" y={G.GROUND_Y} width="205" height="9" fill="var(--concrete-dark)" />

			<!-- house: walls under a roof that overhangs them -->
			<rect x="440" y="120" width="520" height={G.GROUND_Y - 120} fill="var(--wall)" />
			<rect
				x="440"
				y="120"
				width="520"
				height={G.GROUND_Y - 120}
				fill="none"
				stroke="var(--concrete-dark)"
				stroke-opacity="0.5"
			/>
			<path d="M 404 128 L 700 38 L 996 128 Z" fill="var(--concrete)" />
			<path
				d="M 404 128 L 700 38 L 996 128"
				fill="none"
				stroke="var(--concrete-dark)"
				stroke-width="2"
			/>
			<rect x="862" y="52" width="26" height="46" fill="var(--concrete-dark)" />
			<g class="window">
				<rect x="500" y="152" width="66" height="60" />
				<rect x="620" y="152" width="66" height="60" />
				<rect x="800" y="152" width="66" height="60" />
			</g>

			<!-- the slab the house stands on, which is also the garage ceiling -->
			<rect
				x={G.TRENCH_MOUTH_X}
				y={G.GROUND_Y}
				width={G.OUTER_WALL_X - G.TRENCH_MOUTH_X}
				height={G.SLAB_UNDER - G.GROUND_Y}
				fill="var(--concrete)"
			/>
			<!-- back wall -->
			<rect
				x={G.BACK_WALL_X}
				y={G.GROUND_Y}
				width={G.OUTER_WALL_X - G.BACK_WALL_X}
				height={G.FLOOR_Y + 26 - G.GROUND_Y}
				fill="var(--concrete)"
			/>
			<!-- the ramp slab: the car's own travel curve, stroked thick -->
			<g transform="translate(0,13)">
				<path d={G.RAMP_D} fill="none" stroke="var(--concrete)" stroke-width="26" />
			</g>
			<!-- garage floor, and the trench's outer retaining wall -->
			<rect x="548" y={G.FLOOR_Y} width={G.BACK_WALL_X - 548} height="26" fill="var(--concrete)" />
			<rect x="183" y={G.GROUND_Y} width="22" height="96" fill="var(--concrete)" />

			<!-- the excavated void -->
			<path d={G.VOID_D} fill="url(#void)" />
			<path d={G.RAMP_D} fill="none" class="surface-line" />
			<line x1="548" y1={G.FLOOR_Y} x2={G.BACK_WALL_X} y2={G.FLOOR_Y} class="surface-line" />
			<line
				x1={G.TRENCH_MOUTH_X}
				y1={G.SLAB_UNDER}
				x2={G.BACK_WALL_X}
				y2={G.SLAB_UNDER}
				class="surface-line"
			/>

			<!-- everything inside the garage is clipped to the void, so nothing
			     spills out through the concrete -->
			<g clip-path="url(#voidClip)">
				<g class="cell" class:dead={scene.cellular.dead}>
					<path class="cell-halo" d={cellD} />
					<path class="cell-line" d={cellD} />
				</g>
			</g>

			<!-- the car, the phone, and the link between them -->
			<g transform="translate({pose.x.toFixed(2)},{pose.y.toFixed(2)})">
				<g transform="rotate({pose.angle.toFixed(2)})">
					<!-- shadow -->
					<ellipse cx="0" cy="-2" rx="106" ry="7" fill="rgba(0,0,0,0.22)" />
					<path
						class="car-body"
						d="M -110 -30 L -110 -54 C -108 -64 -99 -69 -86 -71 L -56 -73
						   C -38 -99 -10 -107 20 -107 L 36 -107 C 58 -105 78 -90 90 -73
						   L 106 -69 C 114 -65 116 -56 116 -46 L 116 -30 Z"
					/>
					<path
						class="glass"
						d="M -48 -75 C -34 -94 -12 -101 14 -101 L 26 -101 L 20 -75 Z"
					/>
					<path class="glass" d="M 32 -101 C 52 -99 70 -88 80 -75 L 30 -75 Z" />
					<path class="car-seam" d="M -22 -73 L -22 -31" />
					<path class="car-seam" d="M -6 -66 h 14" />
					<rect class="lamp" x="106" y="-56" width="10" height="9" rx="2" />
					<rect class="lamp tail" x="-110" y="-52" width="9" height="8" rx="2" />
					<!-- the dashboard, which stays lit in every state -->
					<g filter="url(#lit)">
						<rect class="dash" x="52" y="-74" width="34" height="19" rx="3" />
					</g>
					<g class="wheel">
						<circle cx="-62" cy="-27" r="27" />
						<circle class="rim" cx="-62" cy="-27" r="12" />
						<circle cx="64" cy="-27" r="27" />
						<circle class="rim" cx="64" cy="-27" r="12" />
					</g>
				</g>

				<!-- never tipped with the car: the phone and the local link -->
				<path class="carplay-halo" d={carplayD} />
				<path class="carplay flow" d={carplayD} />

				<g class="phone">
					<rect
						x={G.PHONE.x}
						y={G.PHONE.y}
						width={G.PHONE.w}
						height={G.PHONE.h}
						rx="10"
						class="phone-shell"
					/>
					<rect
						x={G.PHONE.x + 4}
						y={G.PHONE.y + 5}
						width={G.PHONE.w - 8}
						height={G.PHONE.h - 10}
						rx="6"
						class="phone-screen"
					/>
					<!-- a downloaded map still draws with no signal at all -->
					<g class="map">
						<path
							d="M {G.PHONE.x + 6} {G.PHONE.y + 58} h 50
							   M {G.PHONE.x + 22} {G.PHONE.y + 30} v 84
							   M {G.PHONE.x + 6} {G.PHONE.y + 92} l 50 -22"
						/>
					</g>
					<circle class="dot" cx={G.PHONE.x + 22} cy={G.PHONE.y + 58} r="4" />
					<!-- the only thing on the phone that changes -->
					<g class="chip" class:dead={scene.internet.dead}>
						<rect
							x={G.PHONE.x + 9}
							y={G.PHONE.y + 11}
							width={G.PHONE.w - 18}
							height="13"
							rx="6.5"
						/>
						{#if scene.internet.dead}
							<path
								class="chip-mark"
								d="M {G.PHONE.x + 22} {G.PHONE.y + 13.5} l 18 8 M {G.PHONE.x + 40} {G.PHONE
									.y + 13.5} l -18 8"
							/>
						{:else}
							<path
								class="chip-mark"
								d="M {G.PHONE.x + 22} {G.PHONE.y + 21} h 18 M {G.PHONE.x + 26} {G.PHONE.y +
									21} a 5 5 0 0 1 10 0"
							/>
						{/if}
					</g>
				</g>

				<!-- labels that travel with the car -->
				<g class="label">
					<path class="leader" d="M -30 -146 L 52 -70" />
					<text x="-112" y="-150">head unit · the access point</text>
					<text class="muted" x="-112" y="-134">it relays between its clients</text>
					<text class="end" x={G.PHONE.x - 10} y={G.PHONE.y + 22}>iPhone</text>
					<text class="mid carplay-text" x={G.PHONE.x + G.PHONE.w / 2} y="2">
						Wireless CarPlay
					</text>
					<text class="mid muted" x={G.PHONE.x + G.PHONE.w / 2} y="17">
						local link · carries no internet
					</text>
				</g>
			</g>

			<!-- The way out this project adds. Drawn after the car so the air hop
			     to the box reads over the phone instead of disappearing behind it. -->
			{#if scene.box}
				<g class="wire" in:fade={{ duration: 420, delay: 500 }} out:fade={{ duration: 180 }}>
					<path class="wire-line" d="M 924 {G.BOX.y} L 924 {G.SLAB_UNDER}" />
					<path class="wire-buried" d="M 924 {G.SLAB_UNDER} L 924 272 L 440 272" />
					<path class="wire-line" d="M 440 272 L 336 212" />
					<g class="cloud">
						<path
							d="M 272 196 a 22 22 0 0 1 20 -26 a 26 26 0 0 1 48 -2 a 20 20 0 0 1 2 28 Z"
						/>
					</g>
					<path class="wire-air flow" d={boxHopD} />
					<g class="box">
						<rect x={G.BOX.x} y={G.BOX.y} width={G.BOX.w} height={G.BOX.h} rx="5" />
						<circle class="led" cx={G.BOX.x + 12} cy={G.BOX.y + 11} r="3.2" />
						<path
							d="M {G.BOX.x + 24} {G.BOX.y + 26} h 32 M {G.BOX.x + 24} {G.BOX.y + 32} h 22"
							class="vent"
						/>
					</g>
				</g>
			{/if}

			{#if blockedAt}
				<!-- where the cellular link actually stops -->
				<g class="blocked" in:fade={{ duration: 300, delay: 900 }} out:fade={{ duration: 150 }}>
					<circle cx={blockedAt.x} cy={blockedAt.y - 4} r="13" />
					<path
						d="M {blockedAt.x - 6} {blockedAt.y - 10} l 12 12 M {blockedAt.x + 6} {blockedAt.y -
							10} l -12 12"
					/>
					<text x={blockedAt.x - 22} y={blockedAt.y - 22}>no signal down here</text>
				</g>
			{/if}

			<!-- fixed labels -->
			<g class="label">
				<text x="1012" y="282">street level</text>
				<text class="mid cell-text" x="1044" y="322">Cellular</text>
				<text class="mid muted" x="1044" y="338">your internet, up here</text>
				<text class="muted" x="424" y="382">basement garage</text>
				{#if scene.box}
					<g in:fade={{ duration: 400, delay: 560 }}>
						<text class="mid wire-text" x={G.BOX.x + G.BOX.w / 2} y={G.BOX.y + G.BOX.h + 20}>
							Linux box
						</text>
						<text class="mid muted" x={G.BOX.x + G.BOX.w / 2} y={G.BOX.y + G.BOX.h + 35}>
							IPv6 route out
						</text>
						<text class="mid muted" x={G.BOX.x + G.BOX.w / 2} y={G.BOX.y + G.BOX.h + 50}>
							capped 2 Mbit/s
						</text>
						<text class="end wire-text" x="782" y="366">2× over the air</text>
						<text class="mid wire-text" x="700" y="266">Ethernet</text>
						<text class="mid wire-text" x="306" y="238">your internet</text>
						<text class="mid muted" x="306" y="253">wired, from the house</text>
					</g>
				{/if}
			</g>
		</svg>
	</div>

	<p class="pan-hint" aria-hidden="true">Drag the drawing sideways to see all of it</p>

	<figcaption>
		<div class="readout">
			<span class="pill up">
				<i class="sw carplay-bg"></i>CarPlay&nbsp;<strong>connected</strong>
			</span>
			<span class="pill" class:down={scene.internet.dead}>
				<i class="sw" class:cell-bg={!scene.box} class:wire-bg={scene.box}></i>Internet&nbsp;<strong
					>{scene.internet.label}</strong
				>
			</span>
		</div>

		{#key scene.id}
			<div class="copy" in:fade={{ duration: 260, delay: 90 }}>
				<h3>{scene.title}</h3>
				<p>{scene.caption}</p>
			</div>
		{/key}
	</figcaption>
</figure>

<style>
	.scene {
		margin: 0;
	}

	/* --- the control --- */
	.tabs {
		display: flex;
		gap: 4px;
		padding: 4px;
		margin: 0 auto 14px;
		width: fit-content;
		max-width: 100%;
		background: color-mix(in srgb, var(--ink) 7%, transparent);
		border-radius: 999px;
		flex-wrap: wrap;
		justify-content: center;
	}
	.tabs button {
		appearance: none;
		border: 0;
		background: none;
		font: inherit;
		font-size: 0.93rem;
		font-weight: 550;
		color: var(--ink-soft);
		padding: 0.42em 1.1em;
		border-radius: 999px;
		cursor: pointer;
		transition:
			background 0.18s,
			color 0.18s;
		white-space: nowrap;
	}
	.tabs button:hover {
		color: var(--ink);
	}
	.tabs button.active {
		background: var(--paper-raised);
		color: var(--ink);
		box-shadow: 0 1px 3px rgba(0, 0, 0, 0.14);
	}
	.tabs button:focus-visible {
		outline: 2px solid var(--carplay);
		outline-offset: 2px;
	}

	/* --- the drawing --- */
	.frame {
		border: 1px solid var(--rule);
		border-radius: 12px;
		background: var(--paper-raised);
		overflow-x: auto;
		overflow-y: hidden;
	}
	svg {
		display: block;
		width: 100%;
		min-width: 720px;
		height: auto;
	}

	.grade {
		stroke: var(--concrete-dark);
		stroke-width: 1.5;
	}
	.surface-line {
		stroke: var(--concrete-dark);
		stroke-width: 2;
		stroke-opacity: 0.8;
		fill: none;
	}
	.window rect {
		fill: var(--sky);
		stroke: var(--concrete-dark);
		stroke-opacity: 0.6;
	}

	.tower path {
		fill: none;
		stroke: var(--ink-faint);
		stroke-width: 3;
	}
	.tower .brace {
		stroke-width: 1.2;
		stroke-opacity: 0.6;
	}
	.tower .antenna {
		stroke-width: 4;
		stroke: var(--cellular);
	}

	.fan path {
		fill: none;
		stroke: var(--cellular);
		stroke-width: 3;
		stroke-linecap: round;
		animation: pulse 1.8s ease-out infinite;
	}
	.fan.dead path {
		stroke: var(--ink-faint);
		opacity: 0.25;
		animation: none;
	}
	@keyframes pulse {
		0% {
			opacity: 0;
		}
		35% {
			opacity: 0.95;
		}
		100% {
			opacity: 0;
		}
	}

	/* --- the two links --- */
	.cell-line {
		fill: none;
		stroke: var(--cellular);
		stroke-width: 3.5;
		stroke-linecap: round;
		stroke-dasharray: 11 13;
		animation: flow 0.95s linear infinite;
	}
	.cell-halo {
		fill: none;
		stroke: var(--cellular);
		stroke-width: 9;
		stroke-opacity: 0.22;
		stroke-linecap: round;
	}
	.cell.dead .cell-line {
		stroke: var(--ink-faint);
		stroke-opacity: 0.4;
		animation: none;
	}
	.cell.dead .cell-halo {
		stroke-opacity: 0;
	}
	.cell path {
		transition:
			stroke 0.5s,
			stroke-opacity 0.5s;
	}

	.carplay {
		fill: none;
		stroke: var(--carplay);
		stroke-width: 4;
		stroke-linecap: round;
		stroke-dasharray: 10 14;
	}
	.carplay-halo {
		fill: none;
		stroke: var(--carplay-soft);
		stroke-width: 12;
		stroke-opacity: 0.55;
		stroke-linecap: round;
	}
	.flow {
		animation: flow 0.9s linear infinite;
	}
	@keyframes flow {
		to {
			stroke-dashoffset: -24;
		}
	}

	/* --- the car --- */
	.car-body {
		fill: var(--ink-soft);
		stroke: var(--ink);
		stroke-width: 1.5;
	}
	.glass {
		fill: var(--sky);
		fill-opacity: 0.85;
	}
	.car-seam {
		stroke: var(--ink);
		stroke-opacity: 0.45;
		stroke-width: 1.5;
		fill: none;
	}
	.lamp {
		fill: #ffe9a8;
	}
	.lamp.tail {
		fill: var(--fail);
	}
	.dash {
		fill: var(--carplay);
	}
	.wheel circle {
		fill: #15181d;
	}
	.wheel .rim {
		fill: var(--concrete);
	}

	/* --- the phone --- */
	.phone-shell {
		fill: #22262d;
	}
	.phone-screen {
		fill: #e8eef6;
	}
	.map path {
		stroke: #b3c0d0;
		stroke-width: 3;
		fill: none;
	}
	.dot {
		fill: var(--carplay);
	}
	.chip rect {
		fill: #cfe0f6;
		transition: fill 0.4s;
	}
	.chip .chip-mark {
		stroke: var(--carplay);
		stroke-width: 2.4;
		stroke-linecap: round;
		fill: none;
		transition: stroke 0.4s;
	}
	.chip.dead rect {
		fill: #f0dcd9;
	}
	.chip.dead .chip-mark {
		stroke: var(--fail);
	}

	/* --- the box and its wire --- */
	.box rect {
		fill: var(--wire);
		stroke: #0f3f30;
		stroke-width: 1.2;
	}
	.box .led {
		fill: #bdf5c8;
	}
	.box .vent {
		stroke: rgba(255, 255, 255, 0.55);
		stroke-width: 2;
		fill: none;
	}
	.wire-line {
		fill: none;
		stroke: var(--wire);
		stroke-width: 3.5;
		stroke-linecap: round;
	}
	.wire-buried {
		fill: none;
		stroke: var(--wire);
		stroke-width: 3;
		stroke-opacity: 0.45;
		stroke-dasharray: 3 6;
	}
	.wire-air {
		fill: none;
		stroke: var(--wire);
		stroke-width: 4;
		stroke-linecap: round;
		stroke-dasharray: 10 14;
	}
	.cloud path {
		fill: var(--wire-soft);
		stroke: var(--wire);
		stroke-width: 2;
	}

	/* --- labels --- */
	.label text {
		font-family: var(--font);
		font-size: 14px;
		font-weight: 550;
		fill: var(--ink);
	}
	.label text.mid {
		text-anchor: middle;
	}
	.label text.end {
		text-anchor: end;
	}
	.label text.muted {
		font-size: 12.5px;
		font-weight: 450;
		fill: var(--ink-faint);
	}
	.label .carplay-text {
		fill: var(--carplay);
	}
	.label .cell-text {
		fill: var(--cellular);
	}
	.label .wire-text {
		fill: var(--wire);
	}
	.leader {
		stroke: var(--ink-faint);
		stroke-width: 1.2;
		fill: none;
	}

	.blocked circle {
		fill: var(--paper-raised);
		stroke: var(--fail);
		stroke-width: 2.6;
	}
	.blocked path {
		stroke: var(--fail);
		stroke-width: 3;
		stroke-linecap: round;
		fill: none;
	}
	.blocked text {
		font-family: var(--font);
		font-size: 13px;
		font-weight: 600;
		fill: var(--fail);
		text-anchor: end;
	}

	/* The scene has a floor below which the labels stop being readable, so on
	   narrow screens it pans inside its frame rather than shrinking. Say so. */
	.pan-hint {
		display: none;
		margin: 8px 0 0;
		font-size: 0.8rem;
		color: var(--ink-faint);
	}
	@media (max-width: 760px) {
		.pan-hint {
			display: block;
		}
	}

	/* --- caption --- */
	figcaption {
		margin-top: 16px;
	}
	.readout {
		display: flex;
		gap: 10px;
		flex-wrap: wrap;
		margin-bottom: 12px;
	}
	.pill {
		display: inline-flex;
		align-items: center;
		gap: 7px;
		font-size: 0.85rem;
		color: var(--ink-soft);
		background: color-mix(in srgb, var(--ink) 6%, transparent);
		border-radius: 999px;
		padding: 0.3em 0.8em;
	}
	.pill strong {
		color: var(--ink);
		font-weight: 600;
	}
	.pill.down strong {
		color: var(--fail);
	}
	.sw {
		display: inline-block;
		flex: 0 0 auto;
		width: 10px;
		height: 10px;
		border-radius: 3px;
		background: var(--ink-faint);
	}
	.carplay-bg {
		background: var(--carplay);
	}
	.cell-bg {
		background: var(--cellular);
	}
	.wire-bg {
		background: var(--wire);
	}
	.pill.down .sw {
		background: var(--ink-faint);
	}

	.copy {
		min-height: 9.5em;
	}
	.copy h3 {
		font-size: 1.3rem;
	}
	.copy p {
		margin: 0;
		color: var(--ink-soft);
		max-width: 68ch;
	}

	@media (max-width: 640px) {
		.copy {
			min-height: 0;
		}
	}
</style>
