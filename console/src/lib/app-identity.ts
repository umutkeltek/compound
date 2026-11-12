/**
 * Client-safe app identity helpers (no node imports): the URL slug, a display
 * name for a raw `product:` id, and a stable glyph hue. Names come from the
 * data at runtime; nothing here hardcodes a product.
 */
export function appSlug(productId: string): string {
  return productId.replace(/^product:/, "");
}

export function humanizeProductId(productId: string): string {
  return appSlug(productId)
    .split(/[-_]/)
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

const APP_HUES = ["#E8590C", "#2563EB", "#0E9F6E", "#7C3AED", "#DB2777", "#0891B2", "#CA8A04", "#4F46E5", "#DC2626", "#059669"];

/** Stable per-app hue for the glyph tile; hyphen and underscore ids of one app get the same hue. */
export function appHue(productId: string): string {
  const key = productId.toLowerCase().replace(/-/g, "_");
  let h = 0;
  for (const ch of key) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return APP_HUES[h % APP_HUES.length];
}
