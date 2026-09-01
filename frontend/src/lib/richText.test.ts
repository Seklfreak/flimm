import { describe, expect, it } from "vitest";
import { segments } from "./richText";

describe("segments", () => {
  it("keeps plain text as it is, line breaks included", () => {
    expect(segments("one\n\ntwo")).toEqual([{ kind: "text", text: "one\n\ntwo" }]);
  });

  it("links a URL and leaves the sentence's punctuation outside it", () => {
    expect(segments("see https://example.com/a?b=1.")).toEqual([
      { kind: "text", text: "see " },
      { kind: "link", text: "https://example.com/a?b=1", href: "https://example.com/a?b=1" },
      { kind: "text", text: "." },
    ]);
  });

  it("leaves an unmatched closing bracket to the sentence", () => {
    expect(segments("(at https://example.com/x).")).toEqual([
      { kind: "text", text: "(at " },
      { kind: "link", text: "https://example.com/x", href: "https://example.com/x" },
      { kind: "text", text: ")." },
    ]);
    // A bracket the URL itself opened stays: Wikipedia does this.
    expect(segments("https://en.wikipedia.org/wiki/Jig_(tool)")).toEqual([
      { kind: "link", text: "https://en.wikipedia.org/wiki/Jig_(tool)", href: "https://en.wikipedia.org/wiki/Jig_(tool)" },
    ]);
  });

  it("gives a bare www. host a scheme", () => {
    expect(segments("www.example.com rocks")).toEqual([
      { kind: "link", text: "www.example.com", href: "https://www.example.com" },
      { kind: "text", text: " rocks" },
    ]);
  });

  it("turns timestamps into seeks, with and without an hour", () => {
    expect(segments("0:00 Intro\n1:02:03 End")).toEqual([
      { kind: "time", text: "0:00", seconds: 0 },
      { kind: "text", text: " Intro\n" },
      { kind: "time", text: "1:02:03", seconds: 3723 },
      { kind: "text", text: " End" },
    ]);
  });

  it("does not seek past the end of the video", () => {
    expect(segments("at 2:30 and 0:45", 90)).toEqual([
      { kind: "text", text: "at 2:30 and " },
      { kind: "time", text: "0:45", seconds: 45 },
    ]);
  });

  it("rejects what only looks like a time", () => {
    expect(segments("1:75 v2:30 John 3:16b 10:30:45:12")).toEqual([
      { kind: "text", text: "1:75 v2:30 John 3:16b 10:30:45:12" },
    ]);
  });

  it("lets a link keep a timestamp that sits inside it", () => {
    expect(segments("https://example.com/watch/1:30 and 1:30")).toEqual([
      { kind: "link", text: "https://example.com/watch/1:30", href: "https://example.com/watch/1:30" },
      { kind: "text", text: " and " },
      { kind: "time", text: "1:30", seconds: 90 },
    ]);
  });
});
