<script lang="ts">
	import { fade } from 'svelte/transition';
	import PhysicalScene from './PhysicalScene.svelte';
	import NetworkInset from './NetworkInset.svelte';
	import { SCENES, DEFAULT_SCENE, type SceneId } from './scenes';

	let activeId = $state<SceneId>(DEFAULT_SCENE);
	const scene = $derived(SCENES.find((s) => s.id === activeId) ?? SCENES[0]);
	const paragraphs = $derived(scene.body.split('\n'));
</script>

<section class="diagram">
	<div class="tabs" role="radiogroup" aria-label="Where the car is">
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

	<div class="views">
		<PhysicalScene {scene} />
		<NetworkInset {scene} />
	</div>

	<ul class="legend">
		<li><span class="k phys"></span>physical link — Wi-Fi association or cable</li>
		<li><span class="k carplay"></span>CarPlay traffic</li>
		<li><span class="k internet"></span>internet traffic</li>
		<li><span class="k dead"></span>link present, not working</li>
	</ul>

	<div class="panel">
		<div class="status">
			<span class="chip up">
				<span class="dot carplay-bg"></span>
				<span class="k-label">CarPlay</span>
				<strong>{scene.carplay.label}</strong>
			</span>
			<span class="chip" class:down={scene.internet.state === 'down'}>
				<span
					class="dot"
					class:internet-bg={scene.internet.state === 'up'}
					class:dead-bg={scene.internet.state === 'down'}
				></span>
				<span class="k-label">Internet</span>
				<strong>{scene.internet.label}</strong>
			</span>
		</div>

		{#key scene.id}
			<div class="copy" in:fade={{ duration: 220, delay: 80 }}>
				<h2>{scene.title}</h2>
				{#each paragraphs as p, i (i)}
					<p>{p}</p>
				{/each}
			</div>
		{/key}
	</div>
</section>

<style>
	.diagram {
		margin-bottom: clamp(40px, 6vw, 72px);
	}

	.tabs {
		display: flex;
		gap: 4px;
		padding: 4px;
		margin: 0 0 18px;
		width: fit-content;
		max-width: 100%;
		background: color-mix(in srgb, var(--ink) 7%, transparent);
		border-radius: 999px;
		flex-wrap: wrap;
	}
	.tabs button {
		appearance: none;
		border: 0;
		background: none;
		font: inherit;
		font-size: 0.95rem;
		font-weight: 550;
		color: var(--ink-soft);
		padding: 0.46em 1.2em;
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
		box-shadow: 0 1px 3px rgba(0, 0, 0, 0.16);
	}
	.tabs button:focus-visible {
		outline: 2px solid var(--carplay);
		outline-offset: 2px;
	}

	/* The column ratio matches the two viewBox aspect ratios, so both frames
	   render at the same height and neither view is shrunk to fit the other. */
	.views {
		display: grid;
		grid-template-columns: 1.41fr 1fr;
		gap: 18px;
		align-items: start;
	}

	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: 10px 22px;
		list-style: none;
		padding: 0;
		margin: 14px 0 0;
		font-size: 0.85rem;
		color: var(--ink-faint);
	}
	.legend li {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.k {
		display: inline-block;
		width: 26px;
		height: 0;
		border-top-width: 3px;
		border-top-style: solid;
		border-color: var(--link);
	}
	.k.phys {
		border-top-width: 3.5px;
	}
	.k.carplay {
		border-color: var(--carplay);
		border-top-style: dashed;
	}
	.k.internet {
		border-color: var(--internet);
		border-top-style: dashed;
	}
	.k.dead {
		border-color: var(--unavail);
		border-top-style: dashed;
	}

	.panel {
		margin-top: 22px;
		padding: 20px 22px;
		background: var(--panel);
		border: 1px solid var(--rule);
		border-radius: 10px;
	}
	.status {
		display: flex;
		flex-wrap: wrap;
		gap: 10px;
		margin-bottom: 14px;
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 8px;
		font-size: 0.88rem;
		color: var(--ink-soft);
		background: var(--paper-raised);
		border: 1px solid var(--rule);
		border-radius: 999px;
		padding: 0.32em 0.9em;
	}
	.chip strong {
		color: var(--ink);
		font-weight: 650;
	}
	.chip.down strong {
		color: var(--fail);
	}
	.k-label {
		color: var(--ink-faint);
	}
	.dot {
		width: 9px;
		height: 9px;
		flex: 0 0 auto;
		border-radius: 50%;
		background: var(--link);
	}
	.carplay-bg {
		background: var(--carplay);
	}
	.internet-bg {
		background: var(--internet);
	}
	.dead-bg {
		background: var(--unavail);
	}

	.copy {
		min-height: 11em;
	}
	.copy h2 {
		font-size: 1.22rem;
		margin-bottom: 0.45em;
	}
	.copy p {
		margin: 0 0 0.7em;
		color: var(--ink-soft);
		max-width: 72ch;
	}
	.copy p:last-child {
		margin-bottom: 0;
	}

	/* Stack the two views rather than shrinking every label in them. */
	@media (max-width: 900px) {
		.views {
			grid-template-columns: 1fr;
			gap: 22px;
		}
		.copy {
			min-height: 0;
		}
	}
</style>
