# Verified Spectrum CSS names for Grimoire theme work

This file exists so future template/CSS tasks (Tasks 3-7 of the Grimoire Spectrum theme plan) use verified installed Spectrum names instead of guessing.

## Resolved package versions

Verified from `web/theme/node_modules/@spectrum-css/*/package.json`:

- `@spectrum-css/tokens`: `16.0.2`
- `@spectrum-css/typography`: `8.2.0`
- `@spectrum-css/page`: `9.2.0`
- `@spectrum-css/card`: `11.2.0`
- `@spectrum-css/link`: `7.2.0`
- `@spectrum-css/button`: `14.2.0`
- `@spectrum-css/textfield`: `8.2.0`
- `@spectrum-css/divider`: `5.2.0`

## Verification notes

- The installed tokens bundle is at `@spectrum-css/tokens/dist/css/index.css` (not `tokens/dist/index.css`).
- `@spectrum-css/{card,textfield,button,link,divider,typography}/README.md` in the installed packages are stub readmes that only link to upstream docs; the usable class names below were re-verified from the installed CSS selectors in `index.css` / `dist/index.css`.
- Tokens README confirms the base theme classes to toggle on the page shell are `.spectrum`, `.spectrum--light` or `.spectrum--dark`, and `.spectrum--medium` or `.spectrum--large`.

## Key design tokens (representative verified subset)

These names were re-verified from `@spectrum-css/tokens/dist/css/{global-vars,light-vars,dark-vars,medium-vars,large-vars}.css`.

### Color

- `--spectrum-background-base-color` = `var(--spectrum-gray-25)`
- `--spectrum-background-layer-1-color` = `var(--spectrum-gray-50)`
- `--spectrum-background-layer-2-color`
  - light: `var(--spectrum-gray-25)`
  - dark: `var(--spectrum-gray-75)`
- `--spectrum-neutral-content-color-default` = `var(--spectrum-gray-800)`
- `--spectrum-neutral-subdued-content-color-default` = `var(--spectrum-gray-700)`
- `--spectrum-accent-content-color-default` = `var(--spectrum-accent-color-900)`
- `--spectrum-accent-background-color-default`
  - light: `var(--spectrum-accent-color-900)`
  - dark: `var(--spectrum-accent-color-800)`
- `--spectrum-negative-border-color-default` = `var(--spectrum-negative-color-900)`
- `--spectrum-disabled-content-color` = `var(--spectrum-gray-400)`
- `--spectrum-focus-indicator-color` = `var(--spectrum-blue-800)`
- `--spectrum-card-selection-background-color` = `var(--spectrum-gray-100)`
- `--spectrum-static-white-text-color` = `var(--spectrum-white)`
- `--spectrum-static-black-text-color` = `var(--spectrum-black)`

### Spacing

- `--spectrum-spacing-100` = `8px`
- `--spectrum-spacing-200` = `12px`
- `--spectrum-spacing-300` = `16px`
- `--spectrum-spacing-400` = `24px`
- `--spectrum-component-edge-to-text-100`
  - medium: `12px`
  - large: `15px`
- `--spectrum-text-to-visual-100`
  - medium: `6px`
  - large: `8px`

### Radius / border / focus / divider thickness

- `--spectrum-corner-radius-75` = `4px`
- `--spectrum-corner-radius-100` = `8px`
- `--spectrum-corner-radius-200` = `10px`
- `--spectrum-border-width-100` = `1px`
- `--spectrum-focus-indicator-thickness` = `2px`
- `--spectrum-divider-thickness-small` = `1px`
- `--spectrum-divider-thickness-medium` = `2px`
- `--spectrum-divider-thickness-large` = `4px`

### Shadow

- `--spectrum-drop-shadow-color` = `var(--spectrum-drop-shadow-color-100)`
- `--spectrum-drop-shadow-color-100` = `rgba(var(--spectrum-drop-shadow-color-100-rgb), var(--spectrum-drop-shadow-color-100-opacity))`
- `--spectrum-drop-shadow-color-200` = `rgba(var(--spectrum-drop-shadow-color-200-rgb), var(--spectrum-drop-shadow-color-200-opacity))`
- `--spectrum-drop-shadow-color-300` = `rgba(var(--spectrum-drop-shadow-color-300-rgb), var(--spectrum-drop-shadow-color-300-opacity))`
- `--spectrum-drop-shadow-x` = `0px`
- `--spectrum-drop-shadow-y`
  - medium: `1px`
  - large: `2px`
- `--spectrum-drop-shadow-blur`
  - medium: `4px`
  - large: `6px`

### Font / type scale

- `--spectrum-font-family` = `var(--spectrum-sans-font-family-stack)`
- `--spectrum-sans-font-family-stack` = `adobe-clean, var(--spectrum-sans-serif-font-family), "Source Sans Pro", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Ubuntu, "Trebuchet MS", "Lucida Grande", sans-serif`
- `--spectrum-serif-font-family-stack` = `adobe-clean-serif, var(--spectrum-serif-font-family), "Source Serif Pro", Georgia, serif`
- `--spectrum-code-font-family-stack` = `"Source Code Pro", Monaco, monospace`
- `--spectrum-font-size-75`
  - medium: `12px`
  - large: `15px`
- `--spectrum-font-size-100`
  - medium: `14px`
  - large: `17px`
- `--spectrum-font-size-200`
  - medium: `16px`
  - large: `19px`
- `--spectrum-font-size-300`
  - medium: `18px`
  - large: `22px`
- `--spectrum-heading-size-xxs` = `var(--spectrum-font-size-100)`
- `--spectrum-heading-size-xs` = `var(--spectrum-font-size-200)`
- `--spectrum-body-size-s` = `var(--spectrum-font-size-100)`
- `--spectrum-body-size-m` = `var(--spectrum-font-size-200)`
- `--spectrum-detail-size-s` = `var(--spectrum-font-size-50)`
- `--spectrum-detail-size-m` = `var(--spectrum-font-size-75)`

## Card component classes

Verified from `@spectrum-css/card/index.css`.

- Base: `.spectrum-Card`
- Variants/modifiers:
  - `.spectrum-Card--quiet`
  - `.spectrum-Card--horizontal`
  - `.spectrum-Card--gallery`
- Child elements:
  - `.spectrum-Card-preview`
  - `.spectrum-Card-coverPhoto`
  - `.spectrum-Card-body`
  - `.spectrum-Card-header`
  - `.spectrum-Card-title`
  - `.spectrum-Card-subtitle`
  - `.spectrum-Card-content`
  - `.spectrum-Card-description`
  - `.spectrum-Card-footer`
  - `.spectrum-Card-actions`
  - `.spectrum-Card-quickActions`
  - `.spectrum-Card-actionButton`
- State classes:
  - `.is-focused`
  - `.is-selected`
  - `.is-drop-target`

## Button component classes

Verified from `@spectrum-css/button/index.css`.

- Base: `.spectrum-Button`
- Visual variants/modifiers:
  - `.spectrum-Button--primary`
  - `.spectrum-Button--secondary`
  - `.spectrum-Button--accent`
  - `.spectrum-Button--negative`
  - `.spectrum-Button--emphasized`
  - `.spectrum-Button--outline`
  - `.spectrum-Button--fill`
  - `.spectrum-Button--quiet`
  - `.spectrum-Button--staticWhite`
  - `.spectrum-Button--staticBlack`
- Size modifiers:
  - `.spectrum-Button--sizeS`
  - default/no size modifier = medium sizing
  - `.spectrum-Button--sizeL`
  - `.spectrum-Button--sizeXL`
- Behavior/layout modifiers:
  - `.spectrum-Button--iconOnly`
  - `.spectrum-Button--noWrap`
- Child/helper classes:
  - `.spectrum-Button-label`
  - `.spectrum-Icon`
  - `.spectrum-ProgressCircle`
- State classes/attributes:
  - `.is-selected`
  - `.is-disabled`
  - `.is-focused`
  - `.is-pending`
  - `[pending]`

## Textfield / FieldLabel / HelpText classes

Verified from `@spectrum-css/textfield/index.css`.

- Base: `.spectrum-Textfield`
- Modifiers:
  - `.spectrum-Textfield--quiet`
  - `.spectrum-Textfield--multiline`
  - `.spectrum-Textfield--grows`
  - `.spectrum-Textfield--sideLabel`
  - `.spectrum-Textfield--sizeS`
  - default/no size modifier = medium sizing
  - `.spectrum-Textfield--sizeL`
  - `.spectrum-Textfield--sizeXL`
- Child/helper classes:
  - `.spectrum-Textfield-input`
  - `.spectrum-Textfield-validationIcon`
  - `.spectrum-Textfield-characterCount`
  - `.spectrum-FieldLabel`
  - `.spectrum-HelpText`
- State classes:
  - `.is-disabled`
  - `.is-focused`
  - `.is-invalid`
  - `.is-keyboardFocused`
  - `.is-readOnly`
  - `.is-valid`

## Link component classes

Verified from `@spectrum-css/link/index.css`.

- Base: `.spectrum-Link`
- Modifiers:
  - `.spectrum-Link--quiet`
  - `.spectrum-Link--secondary`
  - `.spectrum-Link--staticWhite`
  - `.spectrum-Link--staticBlack`

## Divider component classes

Verified from `@spectrum-css/divider/index.css`.

- Base: `.spectrum-Divider`
- Modifiers:
  - `.spectrum-Divider--sizeS`
  - `.spectrum-Divider--sizeL`
  - `.spectrum-Divider--vertical`
  - `.spectrum-Divider--staticWhite`
  - `.spectrum-Divider--staticBlack`

## Typography classes

Verified from `@spectrum-css/typography/dist/index.css`.

- Wrapper: `.spectrum-Typography`
- Heading:
  - Base: `.spectrum-Heading`
  - Sizes: `.spectrum-Heading--sizeXXS`, `--sizeXS`, `--sizeS`, `--sizeM`, `--sizeL`, `--sizeXL`, `--sizeXXL`, `--sizeXXXL`
  - Style modifiers: `.spectrum-Heading--light`, `.spectrum-Heading--heavy`, `.spectrum-Heading--serif`
  - Inline emphasis helpers: `.spectrum-Heading-emphasized`, `.spectrum-Heading-strong`
- Body:
  - Base: `.spectrum-Body`
  - Sizes: `.spectrum-Body--sizeXS`, `--sizeS`, `--sizeM`, `--sizeL`, `--sizeXL`, `--sizeXXL`, `--sizeXXXL`
  - Style modifier: `.spectrum-Body--serif`
  - Inline emphasis helpers: `.spectrum-Body-emphasized`, `.spectrum-Body-strong`
- Detail:
  - Base: `.spectrum-Detail`
  - Sizes: `.spectrum-Detail--sizeS`, `--sizeM`, `--sizeL`, `--sizeXL`
  - Style modifiers: `.spectrum-Detail--light`, `.spectrum-Detail--serif`
  - Inline emphasis helpers: `.spectrum-Detail-emphasized`, `.spectrum-Detail-strong`
- Code:
  - Base: `.spectrum-Code`
  - Sizes: `.spectrum-Code--sizeXS`, `--sizeS`, `--sizeM`, `--sizeL`, `--sizeXL`
  - Inline emphasis helpers: `.spectrum-Code-emphasized`, `.spectrum-Code-strong`
