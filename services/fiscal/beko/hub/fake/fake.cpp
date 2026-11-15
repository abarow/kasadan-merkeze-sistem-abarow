// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Fake Integration Hub: for testing the hubd bridge and the Go side
// when there is no device. It imitates the device's behavior in the documents: on connecting it gives a
// state callback, accepts the basket (1), reports the sale state (tag 3) with a
// callback. If "RED" appears in the name of a product in the basket the sale
// fails (status -1), if "SESSIZ" appears no response comes at all. The state's
// "basketHex" field is the bytes of the basket that reached the device (for the encoding check).
#include <chrono>
#include <cstdio>
#include <string>
#include <thread>

#include "IntegrationHubWrapper.h"

namespace {
SerialInCallback serial_cb = nullptr;
DeviceStateCallback state_cb = nullptr;
int receipts = 0;

std::string hex(const std::string &s) {
    std::string out;
    char b[3];
    for (unsigned char c : s) {
        std::snprintf(b, sizeof b, "%02x", c);
        out += b;
    }
    return out;
}

std::string field(const std::string &json, const std::string &key) {
    size_t k = json.find("\"" + key + "\"");
    if (k == std::string::npos) return "";
    size_t q1 = json.find('"', json.find(':', k) + 1);
    size_t q2 = json.find('"', q1 + 1);
    return json.substr(q1 + 1, q2 - q1 - 1);
}
}  // namespace

extern "C" ConnectionWrapper *createCommunication(std::string) {
    static int dummy;
    std::thread([] {
        std::this_thread::sleep_for(std::chrono::milliseconds(50));
        if (state_cb) state_cb(true, "FAKE-X30TR");
    }).detach();
    return &dummy;
}
extern "C" void deleteCommunication(ConnectionWrapper *) {}
extern "C" void reconnect(ConnectionWrapper *) {}
extern "C" int getActiveDeviceIndex(ConnectionWrapper *) { return 0; }

extern "C" int sendBasket(ConnectionWrapper *, std::string json) {
    std::string id = field(json, "basketID");
    bool decline = json.find("RED") != std::string::npos;
    bool silent = json.find("SESSIZ") != std::string::npos;
    if (!silent) {
        std::thread([id, decline, json] {
            std::this_thread::sleep_for(std::chrono::milliseconds(20));
            int no = decline ? 0 : ++receipts;
            std::string st = "{\"basketID\":\"" + id + "\",\"documentType\":0,\"message\":\"" + (decline ? "RED" : "OK") +
                             "\",\"paymentCount\":1,\"receiptNo\":" + std::to_string(no) +
                             ",\"status\":" + (decline ? "-1" : "0") + ",\"zNo\":7,\"basketHex\":\"" + hex(json) + "\"}";
            if (serial_cb) serial_cb(3, st);
        }).detach();
    }
    return 1;
}
extern "C" int sendPayment(ConnectionWrapper *, std::string) { return 1; }
extern "C" std::string getFiscalInfo(ConnectionWrapper *) {
    return "{\"businessMode\":0,\"pluCount\":0,\"plus\":[],\"sectionCount\":2,\"sections\":[{\"sectionNo\":1,\"taxPercent\":1000},{\"sectionNo\":2,\"taxPercent\":2000}],\"currencies\":[],\"paymentAppInfo\":[67]}";
}
extern "C" void setSerialInCallback(ConnectionWrapper *, SerialInCallback cb) { serial_cb = cb; }
extern "C" void setDeviceStateCallback(ConnectionWrapper *, DeviceStateCallback cb) { state_cb = cb; }
