# The explainer site

A small static site built around one idea: wireless CarPlay and your internet
connection are two separate links, and underground only one of them fails.

    npm install
    npm run dev      # http://localhost:5173
    npm run check    # svelte-check
    npm run build    # static files in build/

To serve it from a subdirectory — GitHub Pages puts this repo under
`/internet-over-wireless-carplay/` — build with the prefix:

    BASE_PATH=/internet-over-wireless-carplay npm run build

## The diagram is two views, not one

`Diagram.svelte` holds the state and renders two coordinated SVGs:

- **`PhysicalScene.svelte`** — an architectural section. It answers *where*:
  car position, ground level, the structure overhead, the tower outside. It
  deliberately carries almost no topology.
- **`NetworkInset.svelte`** — a fixed-position node graph. It answers *who
  talks to whom*. Nothing in it ever moves between states; things appear, and
  the flows along them change, which is what makes the three states
  comparable.

The numbered badge on the car in the physical scene matches the badge on the
inset's title. That is the only tie between them, and it has to stay.

The column ratio in `.views` is derived from the two viewBox aspect ratios so
both frames render at the same height. Change a viewBox and that ratio needs
changing with it.

## Rules the drawing follows

**Colour never carries a distinction alone.** Blue is CarPlay traffic, amber
is internet traffic and the route carrying it, grey plus an explicit ✕ is a
link that exists and is not working. Every one of those also has a label and a
line style, and the palette lives in `app.css` so the prose can reuse it.

**Physical links and traffic flows are drawn differently.** A physical link —
a Wi-Fi association or a cable — is a solid neutral line. Traffic rides a
dashed coloured line offset parallel to it. That is why the "with the box"
state can show CarPlay and internet traffic sharing one Wi-Fi link without
implying they are the same thing.

**Arrowheads are static.** Direction must be readable with the dash animation
off, because `prefers-reduced-motion` turns it off. Watch for keyframes that
end at `opacity: 0`: with motion reduced the element never animates and simply
disappears. The tower's coverage arcs have a resting opacity for that reason.

## The topology is not a guess

The "with the box" state matches the deployment, checked against
`carplayd/carplayd.conf.example` and `docs/ipv6-route.md`:

- The box is a **station on the head unit's AP**, exactly like the phone.
- **There is no direct phone↔box link.** Both are clients of the head unit and
  it relays between them, so internet traffic crosses the air twice. The
  config says so in as many words.
- The backhaul is **wired Ethernet** (`uplink = eth0`) to the house's router.
  The box routes and translates; it does not originate connectivity. Do not
  redraw it as though it did.

## Keeping the claims honest

Scope is a section on the page, not a footnote: two BMWs with factory NBT EVO
head units, one iPhone, one garage, and uneven coverage even within that.
Before changing a number here, check it against `docs/status.md`, which tracks
what is proven and what is not.

One specific piece of wording to leave alone: a downloaded offline map still
routes with no signal at all. What underground costs you is anything *new* — a
search, a route through an area never downloaded, an uncached tile.
