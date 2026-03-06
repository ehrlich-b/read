#!/bin/bash
# Read articles.tsv, compress each via claude -p, post via read post
# TSV format: SOURCE\tTITLE\tLINK\tDATE\tTEXT

READ="$1"
ARTICLES="${2:-/tmp/articles.tsv}"
if [ -z "$READ" ]; then
  echo "usage: $0 /path/to/read [articles.tsv]"
  exit 1
fi

DB="$HOME/.read/read.db"
unset CLAUDECODE
COUNT=0
POSTED=0
SKIPPED=0

while IFS=$'\t' read -r source title link date text; do
  # Skip stderr lines
  [[ "$source" == SKIP* ]] && continue
  [[ "$source" == fetched* ]] && continue
  [ -z "$title" ] && continue

  COUNT=$((COUNT + 1))

  # Skip already-posted URLs before wasting claude calls
  if [ -n "$link" ] && [[ "$link" == http* ]]; then
    escaped_link=$(printf '%s' "$link" | sed "s/'/''/g")
    exists=$(sqlite3 "$DB" "SELECT COUNT(*) FROM social_embeddings WHERE link='$escaped_link';" 2>/dev/null)
    if [ "$exists" -gt 0 ] 2>/dev/null; then
      SKIPPED=$((SKIPPED + 1))
      [ $((SKIPPED % 50)) -eq 0 ] && echo "  skipped $SKIPPED dupes so far..."
      continue
    fi
  fi

  # Skip articles older than 30 days
  if [ -n "$date" ]; then
    article_epoch=$(date -jf "%Y-%m-%dT%H:%M:%SZ" "$date" "+%s" 2>/dev/null || date -jf "%Y-%m-%dT%H:%M:%S%z" "$date" "+%s" 2>/dev/null || echo "")
    if [ -n "$article_epoch" ]; then
      cutoff_epoch=$(date -v-30d "+%s")
      if [ "$article_epoch" -lt "$cutoff_epoch" ] 2>/dev/null; then
        continue
      fi
    fi
  fi

  # Skip articles with paywall/premium signals in source text
  if echo "$text" | grep -qiE "subscribe to read|sign in to continue|premium article|members-only|create a free account|register to read|exclusive to subscribers"; then
    echo "[$COUNT] SKIP (paywall): $source: $title"
    continue
  fi

  # Skip articles with too little text for meaningful compression
  if [ ${#text} -lt 100 ]; then
    echo "[$COUNT] SKIP (too short: ${#text} chars): $source: $title"
    continue
  fi

  echo "[$COUNT] $source: $title"

  # Compress via claude -p
  compressed=$(echo "$text" | claude -p "Compress this article excerpt into a dense, informative summary of max 800 characters. Include the key insight or finding. Start with the article title and source in brackets. No preamble.

Title: $title
Source: $source" --model claude-sonnet-4-6 2>/dev/null)

  if [ -z "$compressed" ]; then
    echo "  SKIP: compression failed"
    continue
  fi

  # Filter bot refusals and paywalled/broken sources
  if echo "$compressed" | grep -qiE "I need (permission|to see|to fetch|the article|the full|more|your permission|approval)|I don't have access|unable to access|sign in to read|subscribe to continue|paywall|members only|premium content|login required|403 Forbidden|access denied|couldn't retrieve|I'd be happy to help.*(but|however)|Could you (provide|paste|share)|Since that needs approval|Let me do the compression|Actually, let me just"; then
    echo "  SKIP: bot refusal/paywalled"
    continue
  fi

  # Truncate to 1024 chars
  compressed="${compressed:0:1024}"

  # Score via claude (using original text, not compressed)
  scorer_prompt=$(sed '1,/^---$/d; 1,/^---$/d' "$(dirname "$0")/../skills/scorer.md")
  score_output=$(echo -e "Title: $title\nSource: $source\n\n$text" | claude -p "$scorer_prompt" --model claude-sonnet-4-6 2>&1)

  # Extract mass from SCORE line, default to 10
  mass=$(echo "$score_output" | grep -oE 'SCORE [0-9]+' | head -1 | awk '{print $2}')
  if [ -z "$mass" ] || [ "$mass" -lt 1 ] 2>/dev/null; then
    mass=10
  fi
  if [ "$mass" -gt 10000 ] 2>/dev/null; then
    mass=10000
  fi
  echo "  scored: $mass"

  echo "  compressed: ${#compressed} chars"
  echo "  date: $date"

  # Build post command
  post_args=("$READ" post --mass "$mass")
  if [ -n "$title" ]; then
    post_args+=(--title "$title")
  fi
  if [ -n "$link" ] && [[ "$link" == http* ]]; then
    post_args+=(--link "$link")
  fi
  if [ -n "$date" ]; then
    post_args+=(--date "$date")
  fi
  post_args+=("$compressed")

  # Post via read post
  output=$("${post_args[@]}" 2>&1)
  echo "  $output"
  POSTED=$((POSTED + 1))

  # Throttle
  sleep 0.1

done < "$ARTICLES"

echo ""
echo "done: $POSTED posted, $SKIPPED skipped (dupes), $COUNT total"
