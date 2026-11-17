/**
 * Geometry for the physical view: an architectural section showing where the
 * car is, and what is between it and the sky. Topology lives in the network
 * inset, not here — this view answers "where", not "who talks to whom".
 *
 * The ramp the car drives down and the curve it is animated along are the
 * same cubic, so the car cannot drift off its own driveway. The cubic's
 * tangents are horizontal at both ends — the transition curves a real ramp is
 * built with — so there is no kink at the top or the bottom of the slope.
 */

export type Pt = { x: number; y: number };
export type Pose = { x: number; y: number; angle: number };

export const VIEW = { w: 760, h: 560 };

export const GROUND_Y = 300;
/** Underside of the slab the house stands on; the garage ceiling. */
export const SLAB_UNDER = 348;
/** Driving surface of the garage floor. */
export const FLOOR_Y = 490;
export const FLOOR_BOTTOM = 520;

export const HOUSE = { x0: 360, x1: 640, yTop: 150 };
export const ROOF = { left: 336, right: 664, apex: { x: 500, y: 88 }, eave: 158 };
export const BACK_WALL = { x0: 640, x1: 664 };
/** The ramp runs in an open trench until it passes under the house here. */
export const TRENCH_MOUTH_X = 360;

const RAMP = {
	p0: { x: 150, y: GROUND_Y },
	p1: { x: 235, y: GROUND_Y },
	p2: { x: 275, y: FLOOR_Y },
	p3: { x: 360, y: FLOOR_Y }
} as const;

export const RAMP_D =
	`M ${RAMP.p0.x} ${RAMP.p0.y}` +
	` C ${RAMP.p1.x} ${RAMP.p1.y} ${RAMP.p2.x} ${RAMP.p2.y} ${RAMP.p3.x} ${RAMP.p3.y}`;

/** The excavated void: open trench, then the roofed garage. */
export const VOID_D =
	`M ${RAMP.p0.x} ${GROUND_Y}` +
	` C ${RAMP.p1.x} ${RAMP.p1.y} ${RAMP.p2.x} ${RAMP.p2.y} ${RAMP.p3.x} ${RAMP.p3.y}` +
	` L ${BACK_WALL.x0} ${FLOOR_Y}` +
	` L ${BACK_WALL.x0} ${SLAB_UNDER}` +
	` L ${TRENCH_MOUTH_X} ${SLAB_UNDER}` +
	` L ${TRENCH_MOUTH_X} ${GROUND_Y}` +
	` Z`;

const DRIVEWAY_X0 = 0;

function cubicAt(t: number): Pt {
	const u = 1 - t;
	const { p0, p1, p2, p3 } = RAMP;
	return {
		x: u * u * u * p0.x + 3 * u * u * t * p1.x + 3 * u * t * t * p2.x + t * t * t * p3.x,
		y: u * u * u * p0.y + 3 * u * u * t * p1.y + 3 * u * t * t * p2.y + t * t * t * p3.y
	};
}

function cubicSlopeAt(t: number): Pt {
	const u = 1 - t;
	const { p0, p1, p2, p3 } = RAMP;
	return {
		x: 3 * u * u * (p1.x - p0.x) + 6 * u * t * (p2.x - p1.x) + 3 * t * t * (p3.x - p2.x),
		y: 3 * u * u * (p1.y - p0.y) + 6 * u * t * (p2.y - p1.y) + 3 * t * t * (p3.y - p2.y)
	};
}

/** Arc-length table, so the car travels the slope at a steady speed. */
const SAMPLES = 160;
const arc: { t: number; s: number }[] = (() => {
	const table: { t: number; s: number }[] = [{ t: 0, s: 0 }];
	let s = 0;
	let prev = cubicAt(0);
	for (let i = 1; i <= SAMPLES; i++) {
		const t = i / SAMPLES;
		const p = cubicAt(t);
		s += Math.hypot(p.x - prev.x, p.y - prev.y);
		table.push({ t, s });
		prev = p;
	}
	return table;
})();

const FLAT_IN = RAMP.p0.x - DRIVEWAY_X0;
const RAMP_LEN = arc[arc.length - 1].s;

/** Where the car rests in each state, as a distance along its travel line. */
export const S_SURFACE = 86 - DRIVEWAY_X0;
export const S_GARAGE = FLAT_IN + RAMP_LEN + (474 - RAMP.p3.x);

/** Wheel contact point and body angle at travel distance `s`. */
export function poseAt(s: number): Pose {
	if (s <= FLAT_IN) return { x: DRIVEWAY_X0 + s, y: GROUND_Y, angle: 0 };
	if (s >= FLAT_IN + RAMP_LEN) {
		return { x: RAMP.p3.x + (s - FLAT_IN - RAMP_LEN), y: FLOOR_Y, angle: 0 };
	}

	const target = s - FLAT_IN;
	let hi = 1;
	while (hi < arc.length && arc[hi].s < target) hi++;
	const a = arc[hi - 1];
	const b = arc[hi];
	const span = b.s - a.s;
	const t = a.t + (b.t - a.t) * (span > 0 ? (target - a.s) / span : 0);

	const p = cubicAt(t);
	const d = cubicSlopeAt(t);
	return { x: p.x, y: p.y, angle: (Math.atan2(d.y, d.x) * 180) / Math.PI };
}

/* ---- the car, in its own coordinates: wheels on y=0, nose to the right ---- */

export const CAR_SCALE = 1.15;

export const CAR = {
	body:
		'M -70 -18 L -70 -34 C -68 -40 -62 -43 -54 -44 L -36 -45' +
		' C -24 -58 -6 -62 12 -62 L 22 -62' +
		' C 36 -61 48 -53 56 -44 L 66 -42' +
		' C 71 -39 73 -34 73 -28 L 73 -18 Z',
	sideGlass: 'M -32 -46 C -22 -56 -8 -59 8 -59 L 18 -59 L 15 -46 Z',
	windscreen: 'M 22 -59 C 34 -58 45 -51 52 -46 L 20 -46 Z',
	wheels: [
		{ x: -40, y: -17 },
		{ x: 42, y: -17 }
	],
	r: 17,
	/** Where the "can it reach the tower" ray starts: clear of the callout. */
	roof: { x: 6, y: -70 },
	/** The cabin bracket that ties this car to the enlarged inset. */
	callout: { x: -38, y: -66, w: 100, h: 54 }
};

export const TOWER = { x: 712, base: GROUND_Y, tip: 118, feed: { x: 712, y: 140 } };

/** The box, bracketed to the garage's back wall, clear of the parked car. */
export const BOX = { x: 568, y: 372, w: 56, h: 30 };

/**
 * Where the box's uplink leaves the picture: up the back of the garage,
 * through the slab, into the house. The route marker moves here when the box
 * arrives — the way out is not through the concrete overhead, it is sideways
 * through the box and up the cable.
 */
export const EXIT = { x: BOX.x + BOX.w / 2, y: 276 };

/**
 * The ray from the car's roof toward the tower. Above ground it is a short
 * directional stub; underground it stops dead at the underside of the slab,
 * which is the whole point of the picture.
 */
export function skyRay(
	roof: Pt,
	blocked: boolean
): { to: Pt; hit: boolean; show: boolean } {
	if (blocked) {
		// Straight up: what stops it is the slab directly overhead, and a
		// vertical stub keeps clear of everything else in the garage. While the
		// car is still on the ramp there is nothing meaningful to draw — a ray
		// aimed at the tower from halfway down would sweep across the house —
		// so it stays hidden until the car has actually arrived underneath.
		return {
			to: { x: roof.x, y: SLAB_UNDER },
			hit: true,
			show: roof.y > SLAB_UNDER
		};
	}
	// Unblocked, it points at the tower, which is the thing it reaches. Hidden
	// until the car is back above ground, for the same reason.
	const dx = TOWER.feed.x - roof.x;
	const dy = TOWER.feed.y - roof.y;
	const len = Math.hypot(dx, dy) || 1;
	const t = Math.min(150, len);
	return {
		to: { x: roof.x + (dx / len) * t, y: roof.y + (dy / len) * t },
		hit: false,
		show: roof.y < GROUND_Y - 20
	};
}

/** One arc of the tower's coverage fan, opening down-left over the house. */
export function fanArc(r: number, from = 150, to = 212): string {
	const pt = (deg: number) => {
		const a = (deg * Math.PI) / 180;
		return { x: TOWER.feed.x + r * Math.cos(a), y: TOWER.feed.y + r * Math.sin(a) };
	};
	const s = pt(from);
	const e = pt(to);
	return `M ${s.x.toFixed(1)} ${s.y.toFixed(1)} A ${r} ${r} 0 0 1 ${e.x.toFixed(1)} ${e.y.toFixed(1)}`;
}
