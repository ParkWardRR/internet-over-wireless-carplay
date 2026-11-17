# The explainer, and the hero animation it renders

This is a small SvelteKit site that explains one idea: wireless CarPlay and
your internet connection are two separate links, and underground only one of
them fails.

**It is not deployed anywhere.** It exists to be filmed. `tools/` drives it
through its three states in a headless browser and encodes the result into the
animation at the top of the repository's README. If you are looking for the
prose version of all this, it is the [project wiki]; this directory is the
picture.

[project wiki]: https://github.com/ParkWardRR/internet-over-wireless-carplay/wiki

## Regenerating the hero

Everything runs in one container — a browser to render, ffmpeg to encode — so
nothing has to be installed on the host but podman.

    podman build -t carplay-render -f tools/Containerfile tools/
    podman run --rm -v "$PWD":/work:Z carplay-render sh /work/tools/render.sh

That writes `tools/out/hero-b.webp` and `tools/out/hero-b.gif`. Copy both to
`docs/img/hero.webp` and `docs/img/hero.gif` — the README's `<picture>` offers
the WebP first and falls back to the GIF, so both have to be there.

Variant **b** is what ships: the diagram alone, dark, under a caption bar.
There is a second variant **a** — the page as it really is, light, with the
state tabs visible — kept because it is occasionally the better illustration.
Render it with `render.sh a b`.

### Why it is filmed on a virtual clock

A screenshot takes far longer than a frame interval. Capturing in real time
would sample the animation at uneven moments and the result would judder. So
`capture.mjs` puts Chrome on a virtual clock and advances it by exactly one
frame between screenshots: every frame is evenly spaced no matter how slow the
disk is, and the output is identical on every run.

Everything variant-specific — theme, what is hidden, the caption bar — is
injected by the capture script. The site has no idea it is being filmed, and
should stay that way.

## Working on the site itself

    npm install
    npm run dev      # http://localhost:5173
    npm run check    # svelte-check
    npm run build    # static files in build/

## The diagram is two views, not one

`Diagram.svelte` holds the state and renders two coordinated SVGs:

- **`PhysicalScene.svelte`** — an architectural section. It answers *where*:
  car position, ground level, the structure overhead, the tower outside. It
  deliberately carries almost no topology.
- **`NetworkInset.svelte`** — a fixed-position node graph. It answers *who
  talks to whom*. Nothing in it ever moves between states; things appear, and
  the flows along them change, which is what makes the three states
  comparable.

The numbered badge on the car matches the badge on the inset's title. That is
the only tie between them, and it has to stay.

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

**The house is in the inset in every state.** Its Wi-Fi, its router and its
line to the internet are drawn whether or not the box exists, because the
question a reader arrives with is *why can the phone not just use the house
Wi-Fi?* The answer is on the crossed link from the phone: a Wi-Fi radio is
associated with one network at a time and the phone has one of them, so for as
long as CarPlay runs that radio is on the car's. The repo says this in
`carplayd/shape.go` and `wiki/Hardware.md`. It is also why the box is labelled
*a second radio* rather than *a second station* — that is the point of it.

**The route marker moves; it does not simply recolour.** Underground a red ✕
sits against the slab, labelled *no path out*. When the box arrives the
cellular path is still blocked — nothing has changed overhead — so the marker
travels to where the way out actually is, turning green on the journey and
arriving at the top of the box's uplink as *path out*. Recolouring it in place
would have claimed the concrete had opened. Its label is hidden while it is in
motion, so no text rides across the drawing.

**Nothing is drawn mid-flight that would be wrong.** The car's "can it reach
the tower" ray is hidden while the car is on the ramp — aimed at the tower
from halfway down it swept straight across the house, and a *no path out*
label landed in a window.

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
