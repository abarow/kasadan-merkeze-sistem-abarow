// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// hubd is a very thin bridge that connects Token's Integration Hub library (libIntegrationHub.so)
// to abarow's Go service. The library's interface is C++ signatures that carry
// std::string value parameters; Go cannot call this directly
// (and the services run without CGO). So everything that touches the library is
// in this program, everything else is in Go.
//
// Line: framed messages over stdin/stdout with Go.
//
//   Go → hubd   "<operation> <id> <length>\n<length bytes>"
//   hubd → Go   "<kind> <ref> <length>\n<length bytes>"
//
// Operations: basket, payment (body: JSON, UTF-8), fiscalinfo, device,
// reconnect. Response kinds: ok (ref: request id; body: result), error
// (ref: request id; body: reason), serial (ref: tag; body: the JSON the device
// sent), state (ref: 1 connected, 0 lost; body: device id),
// ready (the library opened), fatal (could not open).
//
// The device expects ISO-8859-9 (Token's example converts the basket this way too); the basket and
// payment bodies are converted from UTF-8 before being sent.

#include <errno.h>
#include <iconv.h>
#include <unistd.h>

#include <cstdlib>
#include <cstring>
#include <mutex>
#include <stdexcept>
#include <string>

#include "IntegrationHubWrapper.h"

namespace {

// The largest size accepted for a body; so that a corrupt frame does not bloat memory.
constexpr size_t kMaxPayload = 4 << 20;

int out_fd = 1;
std::mutex out_mu;

bool write_all(int fd, const char *p, size_t n) {
    while (n > 0) {
        ssize_t w = write(fd, p, n);
        if (w < 0) {
            if (errno == EINTR) continue;
            return false;
        }
        p += w;
        n -= static_cast<size_t>(w);
    }
    return true;
}

// emit writes a frame to Go; callbacks come from the library's own
// threads, the writes are queued.
void emit(const std::string &kind, const std::string &ref, const std::string &payload) {
    std::string frame = kind + " " + ref + " " + std::to_string(payload.size()) + "\n" + payload;
    std::lock_guard<std::mutex> lock(out_mu);
    if (!write_all(out_fd, frame.data(), frame.size())) _exit(1);  // Go gitti
}

void on_serial(int tag, std::string data) { emit("serial", std::to_string(tag), data); }
void on_state(bool connected, std::string device) { emit("state", connected ? "1" : "0", device); }

std::string to_iso8859_9(const std::string &utf8) {
    iconv_t cd = iconv_open("ISO-8859-9//TRANSLIT", "UTF-8");
    if (cd == reinterpret_cast<iconv_t>(-1)) throw std::runtime_error("iconv_open başarısız");
    std::string out(utf8.size() * 2 + 4, '\0');
    char *in = const_cast<char *>(utf8.data());
    char *o = &out[0];
    size_t in_left = utf8.size(), out_left = out.size();
    size_t rc = iconv(cd, &in, &in_left, &o, &out_left);
    iconv_close(cd);
    if (rc == static_cast<size_t>(-1)) throw std::runtime_error("metin ISO-8859-9'a çevrilemedi");
    out.resize(out.size() - out_left);
    return out;
}

// Input, girdiyi tamponlu okur.
class Input {
public:
    bool line(std::string &s) {
        s.clear();
        for (;;) {
            int c = byte();
            if (c < 0) return false;
            if (c == '\n') return true;
            s.push_back(static_cast<char>(c));
            if (s.size() > 256) return false;
        }
    }
    bool exact(std::string &s, size_t n) {
        s.resize(n);
        for (size_t i = 0; i < n; i++) {
            int c = byte();
            if (c < 0) return false;
            s[i] = static_cast<char>(c);
        }
        return true;
    }

private:
    int byte() {
        if (pos_ == len_) {
            ssize_t r;
            do {
                r = read(0, buf_, sizeof buf_);
            } while (r < 0 && errno == EINTR);
            if (r <= 0) return -1;
            pos_ = 0;
            len_ = static_cast<size_t>(r);
        }
        return static_cast<unsigned char>(buf_[pos_++]);
    }
    char buf_[4096];
    size_t pos_ = 0, len_ = 0;
};

}  // namespace

int main(int argc, char **argv) {
    const std::string company = argc > 1 ? argv[1] : "abarow";

    // So that if the library writes to stdout it does not corrupt the frames: the frame channel is on a separate
    // descriptor, stdout is redirected to stderr.
    out_fd = dup(1);
    if (out_fd < 0) return 1;
    dup2(2, 1);

    ConnectionWrapper *hub = createCommunication(company);
    if (!hub) {
        emit("fatal", "0", "createCommunication başarısız (cihaz/USB yetkisi?)");
        return 1;
    }
    setSerialInCallback(hub, on_serial);
    setDeviceStateCallback(hub, on_state);
    emit("ready", "0", "");

    Input in;
    std::string head, payload;
    while (in.line(head)) {
        // "<operation> <id> <length>"
        size_t a = head.find(' '), b = head.rfind(' ');
        if (a == std::string::npos || a == b) {
            emit("error", "0", "çerçeve başlığı bozuk");
            continue;
        }
        const std::string op = head.substr(0, a), id = head.substr(a + 1, b - a - 1);
        char *end = nullptr;
        unsigned long long n = std::strtoull(head.c_str() + b + 1, &end, 10);
        if (*end != '\0' || n > kMaxPayload) {
            emit("error", id, "çerçeve uzunluğu geçersiz");
            break;  // akış senkronu kaybolur; Go yeniden başlatır
        }
        if (!in.exact(payload, static_cast<size_t>(n))) break;

        try {
            if (op == "basket") {
                emit("ok", id, std::to_string(sendBasket(hub, to_iso8859_9(payload))));
            } else if (op == "payment") {
                emit("ok", id, std::to_string(sendPayment(hub, to_iso8859_9(payload))));
            } else if (op == "fiscalinfo") {
                emit("ok", id, getFiscalInfo(hub));
            } else if (op == "device") {
                emit("ok", id, std::to_string(getActiveDeviceIndex(hub)));
            } else if (op == "reconnect") {
                reconnect(hub);
                emit("ok", id, "");
            } else {
                emit("error", id, "bilinmeyen işlem: " + op);
            }
        } catch (const std::exception &e) {
            emit("error", id, e.what());
        }
    }
    deleteCommunication(hub);
    return 0;
}
