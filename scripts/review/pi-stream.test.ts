import { expect, test } from "bun:test";
import { parsePiStream } from "./pi-stream.ts";

const event = (value: unknown) => JSON.stringify(value);
const assistant = (text: string, tokens: number, cost: number) =>
  event({
    type: "message_end",
    message: {
      role: "assistant",
      content: [
        { type: "thinking", thinking: "hm" },
        { type: "text", text },
      ],
      usage: { totalTokens: tokens, cost: { total: cost } },
    },
  });

test("takes the text of the last assistant message and sums usage", () => {
  const stream = [
    event({ type: "agent_start" }),
    assistant("looking", 100, 0.001),
    event({
      type: "message_end",
      message: { role: "user", content: [{ type: "text", text: "x" }] },
    }),
    "not json",
    assistant("final answer", 50, 0.002),
    event({ type: "agent_settled" }),
  ].join("\n");
  expect(parsePiStream(stream)).toEqual({
    text: "final answer",
    usage: { tokens: 150, costUsd: 0.003 },
  });
});

test("an empty stream yields empty text", () => {
  expect(parsePiStream("").text).toBe("");
});
