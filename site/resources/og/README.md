# The share card

`../static/og.png` is what appears when someone posts a ryla.io link to Slack,
Discord, X or LinkedIn. It is 1200×630, the size every one of them expects.

`card.html` is the source it is rendered from. Keeping the source next to the
image is the point: a PNG alone is a dead end the next time the headline or the
install command changes, and the card has to keep saying the same thing the page
says or the link and the page disagree.

## Re-rendering it

Any Chrome will do it, headless, with no dependencies to install:

```sh
chrome --headless --disable-gpu --hide-scrollbars \
  --window-size=1200,630 \
  --screenshot=../static/og.png \
  --virtual-time-budget=6000 \
  --default-background-color=0a0a0aff \
  "file://$PWD/card.html"
```

`--virtual-time-budget` is not optional: without it the screenshot is taken
before Geist has loaded from Google Fonts, and the card ships in a fallback
face. `--default-background-color` avoids a white flash being captured behind
the near-black page.

On macOS the binary is
`/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`, and on Windows
`C:\Program Files\Google\Chrome\Application\chrome.exe`.

## Keeping it honest

The card repeats the headline and the install command from the landing page. If
either changes, this has to change with it — a share card advertising a command
that no longer works is worse than no card at all, because it is the version
most people see and the one nobody tests.
