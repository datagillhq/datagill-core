# Security Policy

DataGill is an early-stage side project with no production deployment. We still take
security reports seriously, especially anything that could affect people who self-host it.

## Reporting a vulnerability

**Please do not open a public issue, discussion, or pull request for security problems.**

Report it privately instead, using either of these:

- **Email:** [email.ozgurcan@gmail.com](mailto:email.ozgurcan@gmail.com), with "DataGill security" in the subject.
- **GitHub:** go to the repository's **Security** tab and choose **Report a vulnerability**.
  Only the maintainers can see these reports.

Please include:

- what the issue is and where it is (file, endpoint, or component)
- steps to reproduce, or a proof of concept
- the impact you think it has

We aim to acknowledge reports within 7 days. Because this is a side project, fixes are best
effort. We'll keep you updated and credit you in the fix unless you'd rather we didn't.

## Scope

In scope: code in this repository, including the search service, worker, workflows,
migrations, and the Docker Compose setup.

Areas we especially care about:

- tenant isolation (one org reading or changing another org's data)
- JWT validation and the GitHub App install callback
- handling of provider credentials (encrypted API keys, GitHub App keys)

Out of scope:

- the separate `datagill-ui` repository (report those issues there)
- the local-development defaults in `.env.example` and `docker-compose.yml`, which are
  intentionally weak and not meant for production

## Supported versions

There are no releases yet. Only the latest `main` is supported.
