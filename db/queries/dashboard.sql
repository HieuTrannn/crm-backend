-- Every query filters by tenant_id explicitly (layer 1); RLS is layer 2.

-- FR-DASH-01: count and total amount per stage in ONE query. Every stage is
-- listed even without deals, in pipeline order. Uses deals_tenant_stage_idx.
-- name: PipelineSummary :many
SELECT s.stage::text                       AS stage,
       count(d.id)                         AS deal_count,
       coalesce(sum(d.amount), 0)::bigint  AS total_amount
FROM unnest('{lead,qualified,proposal,won,lost}'::text[]) WITH ORDINALITY AS s (stage, position)
LEFT JOIN deals d
       ON d.stage = s.stage
      AND d.tenant_id = sqlc.arg(tenant_id)
      AND d.deleted_at IS NULL
GROUP BY s.stage, s.position
ORDER BY s.position;

-- FR-DASH-02: won revenue and win rate per month, for the 6 calendar months in
-- Asia/Ho_Chi_Minh ending with the month of as_of. Months without deals are 0.
-- name: MonthlySales :many
WITH months AS (
    SELECT generate_series(
               date_trunc('month', sqlc.arg(as_of)::timestamptz AT TIME ZONE 'Asia/Ho_Chi_Minh') - interval '5 months',
               date_trunc('month', sqlc.arg(as_of)::timestamptz AT TIME ZONE 'Asia/Ho_Chi_Minh'),
               interval '1 month'
           ) AS month_start
),
closed AS (
    SELECT date_trunc('month', d.closed_at AT TIME ZONE 'Asia/Ho_Chi_Minh') AS month_start,
           d.stage,
           d.amount
    FROM deals d
    WHERE d.tenant_id = sqlc.arg(tenant_id)
      AND d.deleted_at IS NULL
      AND d.stage IN ('won', 'lost')
)
SELECT to_char(m.month_start, 'YYYY-MM')                                 AS month,
       count(c.stage) FILTER (WHERE c.stage = 'won')                     AS won_count,
       count(c.stage) FILTER (WHERE c.stage = 'lost')                    AS lost_count,
       coalesce(sum(c.amount) FILTER (WHERE c.stage = 'won'), 0)::bigint AS won_amount,
       coalesce(
           count(c.stage) FILTER (WHERE c.stage = 'won')::float8
             / nullif(count(c.stage), 0),
           0
       )::float8                                                         AS win_rate
FROM months m
LEFT JOIN closed c ON c.month_start = m.month_start
GROUP BY m.month_start
ORDER BY m.month_start;
