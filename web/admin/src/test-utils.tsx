import { Provider, defaultTheme } from "@adobe/react-spectrum";
import { render } from "@testing-library/react";
import type { ReactElement } from "react";

// renderWithSpectrum wraps ui in the <Provider theme={defaultTheme}> every
// Spectrum component needs (theming, portals, RSP context). Component tests
// should use this instead of repeating the wrapper inline.
export function renderWithSpectrum(ui: ReactElement) {
  return render(<Provider theme={defaultTheme}>{ui}</Provider>);
}
