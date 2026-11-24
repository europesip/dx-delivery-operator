#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 EUROPESIP
# Builds the deprecated alias chart hcl-dx-scriptapp-operator (0.13.x only) from charts/dx-delivery-operator:
# identical templates, CRDs and values, the old chart name, and nameOverride set to the old name so an existing
# release (helm upgrade dxop) keeps its resource names. Output: dist/alias/hcl-dx-scriptapp-operator.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=dist/alias/hcl-dx-scriptapp-operator
rm -rf "$OUT" && mkdir -p "$(dirname "$OUT")" && cp -r charts/dx-delivery-operator "$OUT"
sed -i -e 's/^name: dx-delivery-operator$/name: hcl-dx-scriptapp-operator/' \
       -e 's/^description: >-$/deprecated: true\ndescription: >-/' "$OUT/Chart.yaml"
sed -i 's/^nameOverride: ""/nameOverride: "hcl-dx-scriptapp-operator"/' "$OUT/values.yaml"
sed -i 's/^image:$/image:  # deprecated chart name: use the chart dx-delivery-operator/' "$OUT/values.yaml"
echo "alias chart ready: $OUT"
