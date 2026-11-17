export type SceneId = 'surface' | 'garage' | 'box';

export type Scene = {
	id: SceneId;
	tab: string;
	/** Heading of the stable explanation panel. */
	title: string;
	/** Two short paragraphs. The panel height is fixed, so keep them even. */
	body: string;
	/** Is the car under the house? */
	underground: boolean;
	/** Is the box on the link and routing? */
	box: boolean;
	/** Can the phone reach the tower from where it is? */
	cellular: boolean;
	carplay: { label: string; state: 'up' };
	internet: { label: string; state: 'up' | 'down' };
};

export const SCENES: Scene[] = [
	{
		id: 'surface',
		tab: 'Above ground',
		title: 'Two connections, and they are not related',
		body:
			'The head unit runs a small private Wi-Fi network and the phone joins it. That is all ' +
			'wireless CarPlay is: a cable replacement between phone and dashboard. It carries no ' +
			'internet.\n' +
			'The internet is the phone’s own business, over cellular, on a different radio to a ' +
			'different place. Above ground both work, so nobody notices they are separate.',
		underground: false,
		box: false,
		cellular: true,
		carplay: { label: 'Connected', state: 'up' },
		internet: { label: 'Via cellular', state: 'up' }
	},
	{
		id: 'garage',
		tab: 'In the garage',
		title: 'Only one of them fails',
		body:
			'Underground the phone loses the tower. The CarPlay link is untouched — it never left ' +
			'the car — so the dashboard stays lit, audio keeps playing, and a map already stored on ' +
			'the phone still draws and still routes.\n' +
			'What stops is anything that needs the network: a search, a route through an area with ' +
			'no downloaded map, an uncached tile. CarPlay connected does not mean internet ' +
			'connected.',
		underground: true,
		box: false,
		cellular: false,
		carplay: { label: 'Connected', state: 'up' },
		internet: { label: 'Unavailable', state: 'down' }
	},
	{
		id: 'box',
		tab: 'With the box',
		title: 'A second route, over the link that still works',
		body:
			'A Linux box in the garage joins the same CarPlay network as a second station, next to ' +
			'the phone, and advertises itself as the only IPv6 router on that link. The phone picks ' +
			'the route up by ordinary SLAAC; nothing is configured on the phone or the car.\n' +
			'The box does not make internet. It forwards onto the house’s wired connection. And ' +
			'because phone and box are both clients of the head unit, every packet is relayed by it ' +
			'— across the same air, twice.',
		underground: true,
		box: true,
		cellular: false,
		carplay: { label: 'Connected', state: 'up' },
		internet: { label: 'Via the box, over Ethernet', state: 'up' }
	}
];

export const DEFAULT_SCENE: SceneId = 'surface';
