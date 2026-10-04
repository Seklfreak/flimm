import type { Playlist } from "./api";

type Item = Playlist["items"][number];

// Case- and accent-insensitive: "beyonce" finds "Beyoncé".
function fold(s: string): string {
  return s.normalize("NFD").replace(/\p{M}/gu, "").toLowerCase();
}

// Filters a playlist's items in place, on the client. A playlist's detail
// response already carries every item, and TubeArchivist's search returns at
// most 30 hits across the whole archive, so scoping a search to one playlist
// would miss most of a long one. Every word of the query must appear in the
// title or the channel name, in any order: "winter daughter" finds Daughter's
// "Winter". FlimmKit's PlaylistFilter is the same rule for the Apple apps.
export function filterPlaylistItems(items: Item[], query: string): Item[] {
  const words = fold(query).split(/\s+/).filter(Boolean);
  if (words.length === 0) return items;
  return items.filter((it) => {
    const haystack = fold(`${it.video.title} ${it.video.channel.name}`);
    return words.every((w) => haystack.includes(w));
  });
}
