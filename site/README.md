# The explainer site

A small static site whose point is one drawing: a cutaway house with a
basement garage, and the two independent connections a phone has in it.
Everything else on the page is there to support it.

    npm install
    npm run dev      # http://localhost:5173
    npm run check    # svelte-check
    npm run build    # static files in build/

To serve it from a subdirectory — GitHub Pages puts this repo under
`/internet-over-wireless-carplay/` — build with the prefix:

    BASE_PATH=/internet-over-wireless-carplay npm run build

## Why it is built this way

The scene is **inline SVG in a Svelte component**, not a diagramming
library. There are only three states and a handful of meaningful
connections; what the drawing needs is art direction, not automatic graph
layout.

The one piece of real machinery is in `src/lib/scene/geometry.ts`. The ramp
the car drives down and the curve the car is animated along are **the same
cubic**, so the car cannot drift off its own driveway, and the curve's
tangents are horizontal at both ends — the transition curves a real
basement ramp is built with — so the car eases onto the slope and flattens
onto the garage floor without a visible kink.

Two smaller tricks worth knowing before editing:

- The cellular arc is drawn **twice** with identical geometry: once behind
  the building, and once clipped to the excavated void. The concrete
  occludes the middle for free, so underground you watch the link vanish
  into the slab rather than get truncated by hand.
- The phone and its CarPlay link live in a group that **translates with the
  car but does not rotate with it**, so the phone stays upright on the ramp
  and the link between them never has to be recomputed.

## Keeping it honest

The copy follows the project's rule: claims are labelled, and the wording
about offline Maps is deliberately narrow. A downloaded offline map still
routes with no signal at all — what fails underground is anything *new*: a
search, a route through an area that was never downloaded, an uncached
tile. See `wiki/Home.md`.
