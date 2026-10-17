# PicoClaw

PicoClaw is a lightweight personal AI agent. This glossary fixes the vocabulary
for how a conversation's context is stored, reduced, and backed up — an area
where "history", "summary", and "backup" are easily confused.

## Language

**Turn**:
One complete user → assistant exchange, from the user's message through the
assistant's reply (including any tool calls made along the way).
_Avoid_: Round, exchange

**History**:
The ordered list of recent messages kept in a session and sent to the model as
context.
_Avoid_: Transcript, log, conversation

**Summary**:
A condensed prose digest of older history, kept in place of the messages it
replaced and injected into the system prompt.
_Avoid_: Memory, recap, digest

**Summarize**:
To replace older history with a Summary. Runs automatically when history grows
past a size threshold.
_Avoid_: Compress, condense

**Compact**:
An explicit, user-requested Summarize that runs regardless of the automatic
thresholds. Named `/compact`, with `/compress` as an alias.
_Avoid_: Compress, clean, trim

**Clear**:
To discard a session's History and Summary entirely, leaving the session empty.
Named `/clear`.
_Avoid_: Reset, wipe, compact

**Session**:
The scope a History, Summary, and any pending steering belong to; identified by
a session key and owned by exactly one agent.
_Avoid_: Chat, conversation, thread

**Memory archive**:
A backup restricted to a session's or agent's long-term memory data. Configured
under the memory settings.
_Avoid_: Backup, snapshot

**Full backup**:
A backup of the entire PicoClaw home directory (configuration, sessions,
memory, skills). Configured at the top level of the configuration.
_Avoid_: Memory backup, snapshot
