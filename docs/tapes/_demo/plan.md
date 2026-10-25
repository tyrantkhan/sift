# Billing service rewrite

## Goals
Move invoicing out of the monolith into its own service.

## Data model
- `invoices` table, keyed by `account_id`
- No soft deletes

## Rollout
Big-bang cutover on Friday evening.
