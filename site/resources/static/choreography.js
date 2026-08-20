// The website's motion, in five moves and no more.
//
// Motion is vendored into resources/static rather than fetched from a CDN: the
// whole point of this framework is that one binary is the deployment, and a
// page that phones out for its animation library on every load is not that.
//
// Only four functions are bundled — animate, inView, scroll and stagger — and
// animate comes from motion/mini, which drives the browser's own animation
// engine rather than shipping a JavaScript one. Everything here is opacity and
// transform, so nothing needs the full engine. That choice is the difference
// between 70 kB and 24 kB.
import { animate, inView, scroll, stagger } from "./motion.min.js";

// The design system's own tokens. Motion counts in seconds; CSS counts in
// milliseconds, and that is the only translation.
const OUT = [0.16, 1, 0.3, 1]; // --ease-out
const FAST = 0.12; // --dur-fast
const BASE = 0.18; // --dur-base
const STEP = 0.04; // one stagger step

// How far anything travels. Eight pixels is enough to register as arrival and
// not enough to read as travel.
const RISE = 8;

// A section reveals when it is 15% clear of the bottom edge, so it has already
// settled by the time you are reading it.
const MARGIN = "0px 0px -15% 0px";

// Someone who asked their system for less motion gets none of it. There is no
// reduced variant, because every move here is already the smallest version of
// itself — and nothing on the page is hidden behind a trigger, so a page with
// no animation at all is the same page.
if (matchMedia("(prefers-reduced-motion: reduce)").matches) {
	document.documentElement.dataset.motion = "off";
} else {
	choreograph();
}

wireCopyButtons();

function choreograph() {
	// The hero is the only sequence that runs on load rather than on scroll.
	animate(
		"[data-hero] > *",
		{ opacity: [0, 1], transform: [`translateY(${RISE}px)`, "none"] },
		{ duration: BASE, ease: OUT, delay: stagger(0.05, { startDelay: 0.06 }) },
	);

	// 01 · Reveal. Once, on entry.
	inView(
		"[data-reveal]",
		(el) => {
			animate(
				el,
				{ opacity: [0, 1], transform: [`translateY(${RISE}px)`, "none"] },
				{ duration: BASE, ease: OUT },
			);
		},
		{ margin: MARGIN },
	);

	// 02 · Stagger. Children of a group, capped so it never becomes a wave.
	inView(
		"[data-reveal-group]",
		(group) => {
			const items = [...group.children];
			animate(
				items.slice(0, 6),
				{ opacity: [0, 1], transform: [`translateY(${RISE}px)`, "none"] },
				{ duration: BASE, ease: OUT, delay: stagger(STEP) },
			);
			// Anything past the sixth simply appears. A nine-cell stagger at
			// 40ms is a wave, and a wave is decoration.
			const rest = items.slice(6);
			if (rest.length) {
				animate(rest, { opacity: [0, 1] }, { duration: FAST, ease: OUT });
			}
		},
		{ margin: MARGIN },
	);

	// 05 · The only scroll-linked element on the page. No duration: it is tied
	// to position rather than to time, which is what makes it a progress bar
	// and not an animation.
	scroll(animate("[data-progress]", { transform: ["scaleX(0)", "scaleX(1)"] }, { ease: "linear" }));
}

// The install command is the page's primary action, so it is a real button
// rather than something to select by hand.
function wireCopyButtons() {
	for (const box of document.querySelectorAll("[data-copy]")) {
		const button = box.querySelector(".install-copy");
		const code = box.querySelector(".install-cmd");
		if (!button || !code) continue;

		button.addEventListener("click", async () => {
			try {
				await navigator.clipboard.writeText(code.textContent.trim());
			} catch {
				// Clipboard access can be refused, and a page that silently does
				// nothing is worse than one that admits it. Fall back to
				// selecting the text so the keyboard still works.
				const range = document.createRange();
				range.selectNodeContents(code);
				const selection = getSelection();
				selection.removeAllRanges();
				selection.addRange(range);
				return;
			}
			box.classList.add("copied");
			setTimeout(() => box.classList.remove("copied"), 1200);
		});
	}
}
