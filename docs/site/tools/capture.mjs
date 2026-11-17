/**
 * Render the explainer's three states to a PNG frame sequence.
 *
 * Frames are captured on Chrome's *virtual* clock, not in real time. A
 * screenshot takes far longer than a frame interval, so real-time capture
 * would sample the animation at uneven moments and the result would judder.
 * Under a virtual clock the page only advances when we tell it to, by exactly
 * one frame, so every frame is evenly spaced no matter how slow the encoder
 * or the disk is.
 *
 *   node capture.mjs --variant a --out /work/frames/a
 *
 * Variants are described in VARIANTS below. Everything variant-specific —
 * theme, what is hidden, the caption bar — is injected from here, so the site
 * itself has no idea it is being filmed.
 */

import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { mkdir } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import puppeteer from 'puppeteer-core';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const BUILD = path.resolve(HERE, '..', 'build');

const FPS = 20;
const FRAME_MS = 1000 / FPS;

/**
 * The storyboard, in virtual milliseconds. It ends where it began — car above
 * ground, settled — so the loop does not jump.
 */
const TIMELINE = {
	total: 12500,
	cues: [
		{ at: 2600, tab: 1 }, // down the ramp; cellular dies
		{ at: 6600, tab: 2 }, // the box appears and a route opens
		{ at: 10400, tab: 0 } // back up to the street
	]
};

/** Applied to every variant before its own CSS. */
const BASE_CSS = `
	:root { --font: Inter, 'Liberation Sans', sans-serif !important; }
	html { background: var(--paper); }
`;

const VARIANTS = {
	// The page as it really is, light: reads as an interactive explainer.
	a: {
		theme: 'light',
		width: 1300,
		height: 1000,
		clip: '.diagram',
		clipTo: '.legend',
		css: `
			main > header, .detail, footer { display: none !important; }
			main { padding: 26px 28px !important; }
			.panel { display: none !important; }
		`
	},
	// Diagram only, dark, with a caption bar: reads as a hero graphic.
	b: {
		theme: 'dark',
		width: 1300,
		height: 1000,
		clip: '.views',
		clipTo: '#cap',
		css: `
			main > header, .detail, footer, .tabs, .legend, .panel { display: none !important; }
			main { padding: 24px 28px !important; }
			#cap {
				margin-top: 18px;
				padding: 16px 20px;
				border: 1px solid var(--rule);
				border-radius: 10px;
				background: var(--panel);
				font-family: var(--font);
			}
			#cap .t { font-size: 25px; font-weight: 680; color: var(--ink); letter-spacing: -0.02em; }
			#cap .s { font-size: 17px; color: var(--ink-soft); margin-top: 5px; }
		`,
		caption: true
	}
};

const CAPTIONS = [
	['Above ground', 'CarPlay to the dashboard. Cellular to the internet. Two separate links.'],
	['In the garage', 'The tower is gone. CarPlay is untouched — it never left the car.'],
	['With the box', 'A second station on the same Wi-Fi routes out over the house Ethernet.']
];

function arg(name, fallback) {
	const i = process.argv.indexOf(`--${name}`);
	return i === -1 ? fallback : process.argv[i + 1];
}

const TYPES = {
	'.html': 'text/html',
	'.js': 'text/javascript',
	'.css': 'text/css',
	'.json': 'application/json',
	'.svg': 'image/svg+xml',
	'.png': 'image/png',
	'.woff2': 'font/woff2'
};

function serve(root) {
	const server = createServer(async (req, res) => {
		try {
			let p = decodeURIComponent(req.url.split('?')[0]);
			if (p.endsWith('/')) p += 'index.html';
			let file = path.join(root, p);
			if (!existsSync(file)) file = path.join(root, 'index.html');
			const body = await readFile(file);
			res.writeHead(200, { 'content-type': TYPES[path.extname(file)] ?? 'application/octet-stream' });
			res.end(body);
		} catch {
			res.writeHead(404);
			res.end('not found');
		}
	});
	return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server)));
}

const variantName = arg('variant', 'a');
const variant = VARIANTS[variantName];
if (!variant) throw new Error(`unknown variant ${variantName}`);
const outDir = arg('out', path.join(HERE, 'frames', variantName));
await mkdir(outDir, { recursive: true });

const server = await serve(BUILD);
const port = server.address().port;

const browser = await puppeteer.launch({
	executablePath: process.env.PUPPETEER_EXECUTABLE_PATH ?? '/usr/bin/chromium',
	headless: true,
	args: [
		'--no-sandbox',
		'--hide-scrollbars',
		'--force-color-profile=srgb',
		'--font-render-hinting=none',
		'--disable-lcd-text'
	]
});

const page = await browser.newPage();
await page.setViewport({
	width: variant.width,
	height: variant.height,
	deviceScaleFactor: 2
});

const client = await page.createCDPSession();
await client.send('Emulation.setEmulatedMedia', {
	features: [{ name: 'prefers-color-scheme', value: variant.theme }]
});

await page.goto(`http://127.0.0.1:${port}/`, { waitUntil: 'networkidle0' });
await page.waitForSelector('.views svg');
await page.evaluate(() => document.fonts.ready);

await page.addStyleTag({ content: BASE_CSS + variant.css });

if (variant.caption) {
	await page.evaluate((caps) => {
		const cap = document.createElement('div');
		cap.id = 'cap';
		cap.innerHTML = `<div class="t">${caps[0][0]}</div><div class="s">${caps[0][1]}</div>`;
		document.querySelector('.views').insertAdjacentElement('afterend', cap);
	}, CAPTIONS);
}

// Let layout settle before measuring the crop.
await new Promise((r) => setTimeout(r, 400));

const clip = await page.evaluate(
	(sel, selTo) => {
		const a = document.querySelector(sel).getBoundingClientRect();
		const b = document.querySelector(selTo).getBoundingClientRect();
		const pad = 14;
		return {
			x: Math.max(0, Math.floor(Math.min(a.left, b.left) - pad)),
			y: Math.max(0, Math.floor(Math.min(a.top, b.top) - pad)),
			width: Math.ceil(Math.max(a.right, b.right) - Math.min(a.left, b.left) + pad * 2),
			height: Math.ceil(Math.max(a.bottom, b.bottom) - Math.min(a.top, b.top) + pad * 2)
		};
	},
	variant.clip,
	variant.clipTo
);
console.log(`[${variantName}] clip`, clip);

// From here the page only moves when we say so.
await client.send('Emulation.setVirtualTimePolicy', { policy: 'pause' });

function advance(budget) {
	return new Promise((resolve, reject) => {
		const onExpired = () => {
			client.off('Emulation.virtualTimeBudgetExpired', onExpired);
			resolve();
		};
		client.on('Emulation.virtualTimeBudgetExpired', onExpired);
		client
			.send('Emulation.setVirtualTimePolicy', {
				policy: 'pauseIfNetworkFetchesPending',
				budget,
				maxVirtualTimeTaskStarvationCount: 100000
			})
			.catch(reject);
	});
}

async function setTab(i) {
	await page.evaluate((n) => document.querySelectorAll('.tabs button')[n].click(), i);
	if (variant.caption) {
		await page.evaluate(
			(caps, n) => {
				const cap = document.querySelector('#cap');
				cap.querySelector('.t').textContent = caps[n][0];
				cap.querySelector('.s').textContent = caps[n][1];
			},
			CAPTIONS,
			i
		);
	}
}

const frames = Math.round(TIMELINE.total / FRAME_MS);
let fired = 0;

for (let f = 0; f < frames; f++) {
	const t = f * FRAME_MS;

	while (fired < TIMELINE.cues.length && TIMELINE.cues[fired].at <= t) {
		await setTab(TIMELINE.cues[fired].tab);
		fired++;
	}

	await page.screenshot({
		path: path.join(outDir, `f${String(f).padStart(5, '0')}.png`),
		clip,
		optimizeForSpeed: true
	});

	await advance(FRAME_MS);
	if (f % 40 === 0) console.log(`[${variantName}] frame ${f}/${frames}`);
}

console.log(`[${variantName}] wrote ${frames} frames to ${outDir}`);
await browser.close();
server.close();
