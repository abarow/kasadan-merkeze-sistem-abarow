// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Fake SDK header: only for testing, so that hubd can be built and run
// without Token's library. The declarations are signatures that must be compatible with
// Token's published interface; in the real build Token's own
// IntegrationHubWrapper.h file is used (sdk/token).
#pragma once
#include <string>

typedef void ConnectionWrapper;
typedef void (*SerialInCallback)(int, std::string);
typedef void (*DeviceStateCallback)(bool, std::string);

extern "C" ConnectionWrapper *createCommunication(std::string companyName);
extern "C" void deleteCommunication(ConnectionWrapper *ptr);
extern "C" void reconnect(ConnectionWrapper *ptr);
extern "C" int getActiveDeviceIndex(ConnectionWrapper *ptr);
extern "C" int sendBasket(ConnectionWrapper *ptr, std::string jsonData);
extern "C" int sendPayment(ConnectionWrapper *ptr, std::string jsonData);
extern "C" std::string getFiscalInfo(ConnectionWrapper *ptr);
extern "C" void setSerialInCallback(ConnectionWrapper *ptr, SerialInCallback callback);
extern "C" void setDeviceStateCallback(ConnectionWrapper *ptr, DeviceStateCallback callback);
