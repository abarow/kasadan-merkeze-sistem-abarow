# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# Builds the hubd bridge.
#
#   make        With Token's SDK: Token's IntegrationHubWrapper.h and
#               libIntegrationHub.so files must be in the SDK=../sdk/token folder
#               (they are not in the repository; the license is unclear, get them from Token)
#   make fake   With a fake SDK for testing (build/hubd-fake); Token's library
#               gerekmez
#
# Token's library wants Ubuntu 18.04+ and GLIBC 2.27+, 64-bit only.
CXX      ?= g++
CXXFLAGS ?= -O2 -Wall -Wextra -std=c++17 -pthread
SDK      ?= ../sdk/token

build/hubd: hubd.cpp
	mkdir -p build
	$(CXX) $(CXXFLAGS) -I$(SDK) hubd.cpp -L$(SDK) -lIntegrationHub -Wl,-rpath,'$$ORIGIN' -o $@

fake: build/hubd-fake

build/libIntegrationHub.so: fake/fake.cpp fake/IntegrationHubWrapper.h
	mkdir -p build
	$(CXX) $(CXXFLAGS) -shared -fPIC -Ifake fake/fake.cpp -o $@

build/hubd-fake: hubd.cpp build/libIntegrationHub.so fake/IntegrationHubWrapper.h
	$(CXX) $(CXXFLAGS) -Ifake hubd.cpp -Lbuild -lIntegrationHub -Wl,-rpath,'$$ORIGIN' -o $@

clean:
	rm -rf build

.PHONY: fake clean
