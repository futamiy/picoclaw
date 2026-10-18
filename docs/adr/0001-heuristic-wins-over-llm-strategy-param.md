# Heuristic wins over the LLM strategy hint in hybrid web search

In hybrid `auto` mode, `web_search` exposes a `strategy` hint (`auto`/`conventional`/`reasoning`)
so the model can route queries to the right provider class — news lists to conventional link
search, explanation requests to research synthesis. We decided that a strong keyword signal in
the query (`resolveStrategy`, `pkg/tools/integration/web.go`) **overrides** that hint rather than
merely defaulting around it, because weaker models (e.g. DeepSeek, used on this deployment)
frequently pick the wrong mode and we prefer a deterministic mistake over a model-driven one.

## Considered Options

- **Trust the LLM hint.** Rejected: observable wrong picks (a news-listing query routed to
  research synthesis) with no correction path.
- **Run both pools on any conflict.** Rejected: routing a routine "latest news" query to a paid
  answer engine doubles cost for a query that never needed it.
- **Heuristic only, drop the hint.** Rejected: removes the fine-grained control the hint gives on
  neutral queries the heuristic has no opinion about.

## Consequences

- The heuristic is a hard floor: changing its keyword regexes changes routing for every model,
  not just the current one. Tune `strategyNewsRe`/`strategySynthRe` with that in mind.
- `strategy=reasoning` (single research pool) is only reachable on neutral queries; a query
  containing explanation keywords always routes to `auto` (both pools).
