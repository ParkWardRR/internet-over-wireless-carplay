/**
 * The scene's fixed geometry, in viewBox units.
 *
 * The car's travel line and the drawn ramp come from the same cubic, so the
 * car cannot float above its own driveway. The cubic's tangents are
 * horizontal at both ends, which means the car eases onto the slope and
 * flattens out on the garage floor without a kink — the same transition
 * curves a real basement ramp is built with.
 */

export type Pt = { x: number; y: number };
export type Pose = { x: number; y: number; angle: number };

export const VIEW = { w: 1160, h: 600 };

/** Street level. Everything above is sky, everything below is excavated. */
export const GROUND_Y = 290;
/** Underside of the slab the house sits on — the garage ceiling. */
export const SLAB_UNDER = 355;
/** Driving surface of the garage floor. */
export const FLOOR_Y = 545;

/** The ramp runs in an open trench until it passes under the house here. */
export const TRENCH_MOUTH_X = 400;
/** Inside face of the garage's back wall. */
export const BACK_WALL_X = 960;
export const OUTER_WALL_X = 985;

const RAMP = {
	p0: { x: 205, y: GROUND_Y },
	p1: { x: 330, y: GROUND_Y },
	p2: { x: 430, y: FLOOR_Y },
	p3: { x: 560, y: FLOOR_Y }
} as const;

export const RAMP_D =
	`M ${RAMP.p0.x} ${RAMP.p0.y}` +
	` C ${RAMP.p1.x} ${RAMP.p1.y} ${RAMP.p2.x} ${RAMP.p2.y} ${RAMP.p3.x} ${RAMP.p3.y}`;

/** The excavated void: open trench, then the roofed garage. Clip and fill. */
export const VOID_D =
	`M ${RAMP.p0.x} ${GROUND_Y}` +
	` C ${RAMP.p1.x} ${RAMP.p1.y} ${RAMP.p2.x} ${RAMP.p2.y} ${RAMP.p3.x} ${RAMP.p3.y}` +
	` L ${BACK_WALL_X} ${FLOOR_Y}` +
	` L ${BACK_WALL_X} ${SLAB_UNDER}` +
	` L ${TRENCH_MOUTH_X} ${SLAB_UNDER}` +
	` L ${TRENCH_MOUTH_X} ${GROUND_Y}` +
	` Z`;

const DRIVEWAY_X0 = 30;
const FLOOR_X1 = BACK_WALL_X;

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

/** Arc-length table so the car travels the ramp at a steady speed. */
const SAMPLES = 160;
const arc: { t: number; s: number; p: Pt }[] = (() => {
	const table: { t: number; s: number; p: Pt }[] = [];
	let s = 0;
	let prev = cubicAt(0);
	table.push({ t: 0, s: 0, p: prev });
	for (let i = 1; i <= SAMPLES; i++) {
		const t = i / SAMPLES;
		const p = cubicAt(t);
		s += Math.hypot(p.x - prev.x, p.y - prev.y);
		table.push({ t, s, p });
		prev = p;
	}
	return table;
})();

const FLAT_IN = RAMP.p0.x - DRIVEWAY_X0;
const RAMP_LEN = arc[arc.length - 1].s;
const FLAT_OUT = FLOOR_X1 - RAMP.p3.x;

/** Travel distance of the two resting places, measured from the driveway. */
export const S_SURFACE = 120 - DRIVEWAY_X0;
export const S_GARAGE = FLAT_IN + RAMP_LEN + (640 - RAMP.p3.x);
export const S_MAX = FLAT_IN + RAMP_LEN + FLAT_OUT;

/** Where the car's wheels sit, and how far it is tipped, at travel `s`. */
export function poseAt(s: number): Pose {
	if (s <= FLAT_IN) {
		return { x: DRIVEWAY_X0 + s, y: GROUND_Y, angle: 0 };
	}
	if (s >= FLAT_IN + RAMP_LEN) {
		return { x: RAMP.p3.x + (s - FLAT_IN - RAMP_LEN), y: FLOOR_Y, angle: 0 };
	}

	const target = s - FLAT_IN;
	let hi = 1;
	while (hi < arc.length && arc[hi].s < target) hi++;
	const a = arc[hi - 1];
	const b = arc[hi];
	const span = b.s - a.s;
	const f = span > 0 ? (target - a.s) / span : 0;
	const t = a.t + (b.t - a.t) * f;

	const p = cubicAt(t);
	const d = cubicSlopeAt(t);
	return { x: p.x, y: p.y, angle: (Math.atan2(d.y, d.x) * 180) / Math.PI };
}

/* ---- the car, in its own coordinates: wheels on y=0, nose to the right ---- */

/** Anchor for the links, on the head unit in the dashboard. */
export const HEAD_UNIT: Pt = { x: 70, y: -62 };
/** The enlarged phone, drawn beside the car and never tipped with it. */
export const PHONE = { x: 150, y: -160, w: 62, h: 124 };
export const PHONE_SIDE: Pt = { x: PHONE.x, y: PHONE.y + PHONE.h / 2 };
export const PHONE_TOP: Pt = { x: PHONE.x + PHONE.w / 2, y: PHONE.y };

/* ---- the far end of the cellular link ---- */

export const TOWER = { x: 1080, base: GROUND_Y, tip: 76 };
export const TOWER_FEED: Pt = { x: TOWER.x, y: 96 };

/** One arc of the tower's radiating fan, opening down-left toward the house. */
export function fanArc(r: number, from = 148, to = 214): string {
	const pt = (deg: number) => {
		const a = (deg * Math.PI) / 180;
		return {
			x: TOWER_FEED.x + r * Math.cos(a),
			y: TOWER_FEED.y + r * Math.sin(a)
		};
	};
	const s = pt(from);
	const e = pt(to);
	return `M ${s.x.toFixed(1)} ${s.y.toFixed(1)} A ${r} ${r} 0 0 1 ${e.x.toFixed(1)} ${e.y.toFixed(1)}`;
}

/** The box, bracketed to the garage's back wall. */
export const BOX = { x: 890, y: 372, w: 68, h: 38 };

/**
 * The cellular link, as a quadratic from the phone up to the tower. The same
 * arc is used in every state: what changes is whether it is alive, and the
 * fact that underground it runs straight into the slab.
 */
export function cellularPath(from: Pt): string {
	const cx = (from.x + TOWER_FEED.x) / 2 + 20;
	const cy = Math.max(26, Math.min(from.y, TOWER_FEED.y) - 110);
	return `M ${from.x} ${from.y} Q ${cx} ${cy} ${TOWER_FEED.x} ${TOWER_FEED.y}`;
}

/** Where that arc meets the underside of the slab, i.e. where it is stopped. */
export function cellularBlockedAt(from: Pt): Pt | null {
	if (from.y <= SLAB_UNDER) return null;
	const cx = (from.x + TOWER_FEED.x) / 2 + 20;
	const cy = Math.max(26, Math.min(from.y, TOWER_FEED.y) - 110);
	for (let i = 1; i <= 240; i++) {
		const t = i / 240;
		const u = 1 - t;
		const y = u * u * from.y + 2 * u * t * cy + t * t * TOWER_FEED.y;
		if (y <= SLAB_UNDER) {
			const x = u * u * from.x + 2 * u * t * cx + t * t * TOWER_FEED.x;
			return { x, y };
		}
	}
	return null;
}
