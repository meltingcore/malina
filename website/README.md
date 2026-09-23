# Malina website

Dependency-free static product site for Malina. Preview it locally with:

```sh
python3 -m http.server 4173 --directory website
```

The screenshots under `assets/screenshots/` are captured from Malina's real Wails frontend. The
download links query the public GitHub Releases API and match the archives produced by the release
workflow. They fall back to the releases page if the API or release assets are unavailable.

The operating-system icons under `assets/os-*.svg` are from
[Devicon](https://github.com/devicons/devicon), licensed under the MIT License.

## Cloudflare Pages setup

`.github/workflows/deploy-website.yml` deploys this directory whenever a change under `website/`
reaches `main`.

One-time setup:

1. Create a Direct Upload Pages project named `malina` with production branch `main` using
   `npx wrangler pages project create`.
2. Create an API token with **Account → Cloudflare Pages → Edit** access.
3. Add `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` as GitHub Actions repository secrets.
