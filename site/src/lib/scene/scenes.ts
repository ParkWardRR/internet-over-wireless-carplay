export type SceneId = 'surface' | 'garage' | 'box';

export type Scene = {
	id: SceneId;
	/** Label on the segmented control. */
	tab: string;
	title: string;
	caption: string;
	/** Is the car under the house? */
	underground: boolean;
	/** Is the Linux box on the link? */
	box: boolean;
	/**
	 * Cellular and internet are tracked separately on purpose. With the box
	 * fitted there is still no cellular signal underground — what changed is
	 * that the internet now arrives by another route entirely.
	 */
	cellular: { dead: boolean };
	internet: { label: string; dead: boolean };
};

export const SCENES: Scene[] = [
	{
		id: 'surface',
		tab: 'Above ground',
		title: 'Two connections, not one',
		caption:
			'Wireless CarPlay is a private Wi-Fi network between your phone and the dashboard. ' +
			'Your internet arrives separately, over cellular. Nothing on the car network carries ' +
			'internet — it is a cable replacement, not a hotspot — and above ground you never ' +
			'notice, because the phone has a tower.',
		underground: false,
		box: false,
		cellular: { dead: false },
		internet: { label: 'Cellular', dead: false }
	},
	{
		id: 'garage',
		tab: 'In the garage',
		title: 'One of them fails',
		caption:
			'Park under the house and the tower is gone. CarPlay does not care: the dashboard ' +
			'stays lit and music keeps playing. Maps keeps working too, as far as what it already ' +
			'holds — a downloaded offline map still routes with no signal at all. What stops is ' +
			'anything new: a search, a route through an area you have not downloaded, a map tile ' +
			'that was never cached. CarPlay connected is not internet connected.',
		underground: true,
		box: false,
		cellular: { dead: true },
		internet: { label: 'None', dead: true }
	},
	{
		id: 'box',
		tab: 'With the box',
		title: 'Give it a second way out',
		caption:
			'A small Linux box joins the same CarPlay network as a second client and makes itself ' +
			'the only IPv6 router on the link. The phone picks that up with plain SLAAC and ' +
			'reaches the internet over Ethernet. The car is not modified and the phone’s IP ' +
			'settings are not changed. Two catches: you have to tap Use For Internet once, and ' +
			'every byte crosses the air twice — phone to head unit, head unit to box — which is ' +
			'why the box caps itself at 2 Mbit/s rather than take CarPlay down with it.',
		underground: true,
		box: true,
		cellular: { dead: true },
		internet: { label: 'Ethernet, through the box', dead: false }
	}
];

export const DEFAULT_SCENE: SceneId = 'surface';
