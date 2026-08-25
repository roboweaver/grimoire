import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Without vitest's `globals: true` (which we intentionally don't enable, so
// every test file imports describe/it/expect/vi explicitly like the rest of
// the codebase's style), @testing-library/react's automatic cleanup can't
// detect a global afterEach hook to register itself with, so component trees
// from one test would otherwise still be mounted for the next. Do it
// explicitly here, once, for every test file.
afterEach(() => cleanup());

// React Spectrum's Provider requires matchMedia for its color-scheme
// detection; jsdom doesn't implement it. Every component test that renders a
// Spectrum tree needs this stub, so it lives once in the shared setup file.
if (!window.matchMedia) {
  window.matchMedia = (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }) as unknown as MediaQueryList;
}

// ProseMirror (which powers the TipTap-based RichTextEditor, Req 7) queries
// real layout geometry — element-at-point hit testing and text-range
// bounding boxes — to translate mouse/DOM-mutation events into document
// positions. jsdom doesn't implement layout at all, so these throw
// "not a function" without a stub. None of our tests assert on actual pixel
// geometry, so empty/zeroed results are sufficient to let ProseMirror's event
// handling run without crashing.
if (!document.elementFromPoint) {
  document.elementFromPoint = () => null;
}
const emptyRect: DOMRect = {
  bottom: 0,
  height: 0,
  left: 0,
  right: 0,
  top: 0,
  width: 0,
  x: 0,
  y: 0,
  toJSON: () => ({}),
};
Range.prototype.getBoundingClientRect = () => emptyRect;
Range.prototype.getClientRects = () =>
  ({
    length: 0,
    item: () => null,
    [Symbol.iterator]: function* () {},
  }) as unknown as DOMRectList;
if (!HTMLElement.prototype.getClientRects) {
  HTMLElement.prototype.getClientRects = () =>
    ({
      length: 0,
      item: () => null,
      [Symbol.iterator]: function* () {},
    }) as unknown as DOMRectList;
}

