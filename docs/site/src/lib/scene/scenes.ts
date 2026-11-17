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
			'Wireless CarPlay is a Wi-Fi network. The head unit is the access point and your phone ' +
			'is a station on it, the way a laptop joins a café network. That is the whole of it: ' +
			'a cable replacement between phone and dashboard, carrying no internet.\n' +
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
			'Underground the phone loses the tower. CarPlay is untouched — it never left the car ' +
			'— so the dashboard stays lit and a map already on the phone still routes. What stops ' +
			'is anything needing the network: a search, a route through an area never downloaded, ' +
			'an uncached tile.\n' +
			'The house Wi-Fi is metres away and the phone cannot use it either. A Wi-Fi radio is on ' +
			'one network at a time, and the phone’s is on the car’s — that is what ' +
			'wireless CarPlay is. Joining the house network would mean leaving CarPlay.',
		underground: true,
		box: false,
		cellular: false,
		carplay: { label: 'Connected', state: 'up' },
		internet: { label: 'Unavailable', state: 'down' }
	},
	{
		id: 'box',
		tab: 'With the box',
		title: 'Add a second radio',
		body:
			'The box is the radio the phone does not have. It joins the car network as another ' +
			'station, beside the phone, and advertises itself as the only IPv6 router on that link. ' +
			'The phone takes the route by ordinary SLAAC — without leaving CarPlay, and without ' +
			'anything configured on the phone or the car.\n' +
			'The box does not make internet. It is wired to the house connection, the same one the ' +
			'house Wi-Fi uses. And because phone and box are both clients of the head unit, every ' +
			'packet is relayed by it — across the same air, twice.',
		underground: true,
		box: true,
		cellular: false,
		carplay: { label: 'Connected', state: 'up' },
		internet: { label: 'Via the box, over Ethernet', state: 'up' }
	}
];

export const DEFAULT_SCENE: SceneId = 'surface';
