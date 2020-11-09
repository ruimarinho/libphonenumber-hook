# libphonenumber-webhook

## Introduction

A companion serverless function for automatically keeping [google-libphonenumber](https://github.com/ruimarinho/google-libphonenumber) up-to-date with the latest [upstream](https://github.com/google/libphonenumber) releases.

Whenever a new release tag is published on the upstream repository, a webhook is dispatched by GitHub to an endpoint running a serverless function deployed on [Vercel](https://vercel.com). A staging area is created, updated files are downloaded and a pull request opened. Once tests pass and the pull request is merged, a released is tagged and a GitHub Action publishes the resulting package to [npm](https://www.npmjs.com/package/google-libphonenumber).

## Deployment

Vercel is configured to automatically deploy pushes to master. However, if for some reason you would like to manually deploy code, you can do it via the command-line too.

First, deploy the new code to a staging area:

```sh
vercel
```

Once you're happy with the results, you can publish to production:

```sh
vercel --prod
```

## Testing

To test locally, launch a self-hosted Vercel deployment environment:

```sh
vercel dev
```

You can now use an example available under `test/` to simulate a webhook:

```sh
curl -X POST http://localhost:3000 \
-H 'User-Agent: GitHub-Hookshot/4cd0928' \
-H 'X-GitHub-Event: push' \
-H 'X-GitHub-Delivery: b8e83f2a-0bf0-11e8-885c-b813bbeb8910' \
-H 'Content-Type: application/json' \
-d @test/tag.json
```

## Notes

1. In production, a custom `CNAME` is used so that in the event there is a need to change serveless providers, Google does not have to change its webhook endpoint — I can simply point the DNS somewhere else.
2. Vercel comes with a lot of hard limits on its free tier, most notably:
   1. A maximum of 5MB can be downloaded by the function. A typical libphonenumber release has about 8MB of data when compressed as `.tar.gz`.
   2. A maximum execution time of 15 seconds, which makes it impossible to clone the upstream repository, even when git cloning in shallow mode.

## License

MIT
