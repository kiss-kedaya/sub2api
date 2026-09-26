import { parse, serialize } from 'parse5';

export const MAX_INPUT = 1024 * 1024;
export const MAX_OUTPUT = 8 * 1024 * 1024;
export const FRACTIONS = Object.freeze([0, 0.23, 0.51, 0.79]);
export const CSP = "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src 'none'; font-src 'none'; connect-src 'none'; frame-src 'none'; object-src 'none'; worker-src 'none'; base-uri 'none'; form-action 'none'; sandbox";
const HTML = new Set('html head body title meta style div span p main section article header footer figure figcaption h1 h2 h3 h4 h5 h6 br hr pre code strong em b i ul ol li img'.split(' '));
const SVG = new Set('svg g defs title desc metadata path rect circle ellipse line polyline polygon text tspan textPath marker pattern clipPath mask linearGradient radialGradient stop filter feBlend feColorMatrix feComponentTransfer feComposite feConvolveMatrix feDiffuseLighting feDisplacementMap feDistantLight feDropShadow feFlood feFuncA feFuncB feFuncG feFuncR feGaussianBlur feImage feMerge feMergeNode feMorphology feOffset fePointLight feSpecularLighting feSpotLight feTile feTurbulence image use symbol switch animate animateTransform animateMotion mpath set style'.toLowerCase().split(' '));
const ANIMATIONS = new Set(['animate', 'animatetransform', 'animatemotion', 'set']);
const SVG_NS = 'http://www.w3.org/2000/svg';
const HTML_NS = 'http://www.w3.org/1999/xhtml';

function unsafe() { throw new Error('Unsafe HTML: active or unsupported content'); }

function checkElement(node) {
  const tag = node.tagName.toLowerCase();
  if (![SVG_NS, HTML_NS].includes(node.namespaceURI) || !(node.namespaceURI === SVG_NS ? SVG : HTML).has(tag)) unsafe();
  if (tag === 'meta' && (node.attrs.some(attribute => attribute.name.toLowerCase() === 'http-equiv') || !node.attrs.some(attribute => ['charset', 'name'].includes(attribute.name.toLowerCase())))) unsafe();
  for (const attribute of node.attrs) {
    const name = attribute.name.toLowerCase();
    const value = attribute.value.trim();
    if (name.startsWith('on') || ['srcdoc', 'is', 'nonce', 'autofocus', 'download', 'ping'].includes(name)) unsafe();
    if (attribute.namespace && !['http://www.w3.org/1999/xlink', 'http://www.w3.org/XML/1998/namespace', 'http://www.w3.org/2000/xmlns/'].includes(attribute.namespace)) unsafe();
    if (['href', 'src', 'srcset'].includes(name)) {
      const local = /^#[^\s]*$/.test(value);
      const externalImage = ['img', 'image', 'feimage'].includes(tag) && /^https?:\/\//i.test(value);
      if (value && !local && !externalImage) unsafe();
      if (name === 'srcset') unsafe();
    }
    if (ANIMATIONS.has(tag) && name === 'attributename' && /^(?:on|href$|xlink:href$|src|style$|class$|id$|xmlns)/i.test(value)) unsafe();
    if (ANIMATIONS.has(tag) && name === 'begin' && !/^(?:(?:\d+(?:\.\d+)?|\.\d+)(?:ms|s|min|h)?|indefinite)(?:\s*;\s*(?:(?:\d+(?:\.\d+)?|\.\d+)(?:ms|s|min|h)?|indefinite))*$/i.test(value)) unsafe();
  }
}

export function prepareHTML(raw) {
  const bytes = Buffer.isBuffer(raw) ? raw : Buffer.from(raw);
  if (bytes.length > MAX_INPUT) throw new Error('HTML exceeds 1 MiB');
  let html;
  try { html = new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
  catch { throw new Error('HTML must be valid UTF-8'); }
  if (!html.trim()) throw new Error('HTML is empty');
  const document = parse(html, { scriptingEnabled: false });
  const queue = [document];
  let svgCount = 0;
  let elements = 0;
  while (queue.length) {
    const node = queue.pop();
    if (node.tagName) {
      if (++elements > 10000) throw new Error('HTML has too many elements');
      checkElement(node);
      if (node.tagName === 'svg' && node.namespaceURI === SVG_NS) svgCount++;
    }
    if (node.content) unsafe();
    if (node.childNodes) queue.push(...node.childNodes);
  }
  if (svgCount !== 1) throw new Error('HTML must contain exactly one SVG artwork');
  // Canonical serialization prevents passing unchecked original markup to Chromium.
  return serialize(document);
}

export async function readHTML(stream) {
  const chunks = [];
  let size = 0;
  for await (const chunk of stream) {
    const bytes = Buffer.from(chunk);
    size += bytes.length;
    if (size > MAX_INPUT) throw new Error('HTML exceeds 1 MiB');
    chunks.push(bytes);
  }
  return prepareHTML(Buffer.concat(chunks, size));
}

export function encodeFrames(frames) {
  const json = JSON.stringify(frames);
  if (Buffer.byteLength(json) + 1 > MAX_OUTPUT) throw new Error('Rendered output exceeds 8 MiB');
  return json;
}
