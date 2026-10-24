# DFM mocking

Mock systems DFM does not own. Prefer real DFM code and state inside the test boundary.

## Mock these boundaries when needed

- Shopify clients and GraphQL responses.
- COESP HTTP calls and response payloads.
- SNS publishers and SQS subscribers.
- Time, randomness, filesystem, and network.
- Cognito or external authentication providers.

## Prefer real test state

- Django models and constraints.
- Transactions and tenant filters.
- Parsers, validators, classifiers, builders, processors, repositories, and response builders.
- Retry and idempotency decisions that can run without a real external service.

Use the Django test database when persistence affects behavior. Patch the imported name used by the module under test.

A mock must return the real boundary shape. Avoid conditional mock logic that reimplements production behavior. When the mock needs several branches, a higher seam or a small fake may be clearer.

Assert observable output, local state, published intent, or typed error. Assert an internal call only when making that call is itself the contract.
