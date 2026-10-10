#!/bin/bash
set -e
git clone -q "${DISPATCH_REPO:?set DISPATCH_REPO to a dispatch checkout}" .
