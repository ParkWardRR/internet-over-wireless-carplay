# Wiki source

These files are the source of the project's GitHub wiki at
<https://github.com/ParkWardRR/internet-over-wireless-carplay/wiki>.

They live in the repository on purpose. A GitHub wiki is a separate git repo
that cannot be created by pushing — the first page has to be made in the web UI
— so a wiki drafted only in a scratch directory while waiting for that is a
wiki that can be lost. This one was, once.

Publish with:

    scripts/wiki-sync.sh

File names map to page titles: `Getting-Started.md` becomes *Getting Started*.
`_Sidebar.md` and `_Footer.md` are GitHub's navigation and footer conventions.

Claims here follow the same rule as the rest of the project: label everything
**measured** / **source** / **emulator** / **inferred**, and keep the
conclusions that turned out to be wrong.
