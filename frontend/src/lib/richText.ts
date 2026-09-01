// The text of a description or a comment, split into what it is made of.
//
// YouTube text is plain: no markup, but full of two things a viewer expects to
// act on. A URL should open, and a timestamp ("at 2:30 the jig slips") should
// seek — which is the one thing a video's own page can do with it that YouTube's
// cannot. Everything else is text, kept verbatim, line breaks included.
//
// The rules live here, in one place, and mirror FlimmKit's `RichText` so the
// phone, the iPad and the web all link the same things.

export type Segment =
  | { kind: "text"; text: string }
  | { kind: "link"; text: string; href: string }
  | { kind: "time"; text: string; seconds: number };

// A URL, or a bare `www.` host, up to the next whitespace. Trailing sentence
// punctuation belongs to the sentence, not the link ("see https://x.y."), and
// so does a closing bracket that nothing in the URL opened.
const LINK = /(?:https?:\/\/|www\.)[^\s<>"']+/gi;
// h:mm:ss or m:ss, not glued to a letter, digit or another colon on either
// side, so "12:30:00" is one timestamp and "v2:30" or a Bible verse's "3:16"
// in "John 3:16b" are not.
const TIME = /(?<![\w:])(?:(\d{1,2}):)?(\d{1,2}):(\d{2})(?![\w:])/g;

/** Splits `text` into segments. Timestamps beyond `duration` (seconds) are
 *  left as text: a "1:30" in a one-minute video is not a place in it. Without
 *  a duration every well-formed timestamp counts. */
export function segments(text: string, duration = Infinity): Segment[] {
  const out: Segment[] = [];
  let pos = 0;
  const push = (s: Segment) => {
    if (s.text) out.push(s);
  };
  const matches = [...findLinks(text), ...findTimes(text, duration)].sort((a, b) => a.start - b.start);
  for (const m of matches) {
    // Overlaps happen when a timestamp sits inside a URL (`?t=1:30` is not
    // one, but `/1:30` might be): the link wins, having been found first.
    if (m.start < pos) continue;
    push({ kind: "text", text: text.slice(pos, m.start) });
    out.push(m.segment);
    pos = m.end;
  }
  push({ kind: "text", text: text.slice(pos) });
  return out;
}

interface Found {
  start: number;
  end: number;
  segment: Segment;
}

function findLinks(text: string): Found[] {
  const found: Found[] = [];
  for (const m of text.matchAll(LINK)) {
    let raw = m[0];
    // Strip trailing punctuation, then an unmatched closing bracket, then
    // punctuation again ("(see https://x.y/a).").
    for (;;) {
      const trimmed = raw.replace(/[.,;:!?'"]+$/, "");
      const unbalanced = trimmed.endsWith(")") && !trimmed.includes("(") ? trimmed.slice(0, -1) : trimmed;
      if (unbalanced === raw) break;
      raw = unbalanced;
    }
    if (!raw) continue;
    const href = /^https?:\/\//i.test(raw) ? raw : `https://${raw}`;
    found.push({ start: m.index, end: m.index + raw.length, segment: { kind: "link", text: raw, href } });
  }
  return found;
}

function findTimes(text: string, duration: number): Found[] {
  const found: Found[] = [];
  for (const m of text.matchAll(TIME)) {
    const [raw, h, min, sec] = m;
    const minutes = Number(min);
    const seconds = Number(sec);
    // "1:75" is not a time; with an hour "1:75:00" is not either.
    if (seconds > 59 || (h !== undefined && minutes > 59)) continue;
    const total = (h === undefined ? 0 : Number(h) * 3600) + minutes * 60 + seconds;
    if (total > duration) continue;
    found.push({ start: m.index, end: m.index + raw.length, segment: { kind: "time", text: raw, seconds: total } });
  }
  return found;
}
