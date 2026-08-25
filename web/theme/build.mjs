// Copies the compiled CSS from each vendored @spectrum-css/* package into
// themes/default/static/css/vendor/, so the Go server can serve plain static
// files at runtime with no Node dependency. Node is build-time only.
import { copyFileSync, existsSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const outDir = join(
  here,
  "..",
  "..",
  "themes",
  "default",
  "static",
  "css",
  "vendor",
);
mkdirSync(outDir, { recursive: true });

const packages = [
  "tokens",
  "typography",
  "page",
  "card",
  "link",
  "button",
  "textfield",
  "divider",
];

const manifestLines = [
  "/* GENERATED FILE. Do not edit by hand.",
  " * Run `make theme-css` (web/theme/build.mjs) to regenerate. */",
];

for (const pkg of packages) {
  const packageDir = join(here, "node_modules", "@spectrum-css", pkg);
  const distIndex = join(packageDir, "dist", "index.css");
  const distCssIndex = join(packageDir, "dist", "css", "index.css");
  const src = existsSync(distIndex) ? distIndex : distCssIndex;
  const destName = `${pkg}.css`;
  const dest = join(outDir, destName);
  copyFileSync(src, dest);
  manifestLines.push(`@import url("vendor/${destName}");`);
  console.log(`copied ${pkg} -> themes/default/static/css/vendor/${destName}`);
}

const manifestPath = join(
  here,
  "..",
  "..",
  "themes",
  "default",
  "static",
  "css",
  "spectrum.css",
);
writeFileSync(manifestPath, manifestLines.join("\n") + "\n");
console.log(`wrote ${manifestPath}`);
