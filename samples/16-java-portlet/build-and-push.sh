#!/usr/bin/env bash
# Builds the EUROPESIP Showcase portlet and pushes it as an OCI artifact for DXJavaExtension (issue #54).
#   ./build-and-push.sh <version> <registry/repository> ["what's new" text]
#   e.g. ./build-and-push.sh 1.0.0 harbor.example.com/dx-artifacts/java-extensions/portlets/europesip-showcase-portlet
# Needs Maven + JDK 8 (or Docker for the maven:3.9-eclipse-temurin-8 image) and oras logged in to the registry.
# Use immutable tags (semver): a tag is never rebuilt; the operator always loads the digest it resolved.
set -euo pipefail
VERSION=${1:?version}; REPO=${2:?registry/repository}; WHATSNEW=${3:-}
cd "$(dirname "$0")/showcase-portlet"
if command -v mvn >/dev/null; then
  mvn -q -B clean package -Drevision="$VERSION" "-Dwhatsnew=$WHATSNEW"
else
  docker run --rm -u "$(id -u):$(id -g)" -e MAVEN_CONFIG=/tmp/m2 -v "$PWD":/src -w /src maven:3.9-eclipse-temurin-8 \
    mvn -q -B -Dmaven.repo.local=/tmp/m2 clean package -Drevision="$VERSION" "-Dwhatsnew=$WHATSNEW"
fi
cd target
oras push "$REPO:$VERSION" --artifact-type application/vnd.europesip.dx.java-extension.v1 \
  --annotation "org.opencontainers.image.title=EUROPESIP Showcase portlet" \
  --annotation "org.opencontainers.image.description=JSR 286 sample portlet for DXJavaExtension (DX Delivery Operator)" \
  --annotation "org.opencontainers.image.version=$VERSION" \
  --annotation "org.opencontainers.image.vendor=EUROPESIP" \
  --annotation "org.opencontainers.image.licenses=MIT" \
  --annotation "org.opencontainers.image.source=https://github.com/europesip/dx-delivery-operator/tree/main/samples/16-java-portlet" \
  --annotation "org.opencontainers.image.created=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --annotation "com.europesip.dx.kind=java-extension" \
  --annotation "com.europesip.dx.java.type=portlet" \
  --annotation "com.europesip.dx.java.jsr=286" \
  --annotation "com.europesip.dx.java.context-root=/europesip-showcase" \
  --annotation "com.europesip.dx.java.portlet-uids=ShowcasePortlet" \
  --annotation "com.europesip.dx.java.webapp-uid=com.europesip.showcase.ShowcasePortlet.webmod" \
  --annotation "com.europesip.dx.java.min-cf=CF239" \
  europesip-showcase-portlet.war:application/java-archive
