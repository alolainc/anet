# anet benchmarks

So sánh hiệu năng của `anet` với một số thư viện mạng Go khác trên cùng một bài test echo.

| Thư mục | Thư viện |
|---|---|
| `anet/` | `github.com/alolainc/anet` (bản trong repo này, `replace => ../`) |
| `net/` | thư viện chuẩn `net` của Go (mỗi connection một goroutine) |
| `netpoll/` | `github.com/cloudwego/netpoll` v0.7.5 (thư viện gốc mà anet dẫn xuất) |
| `gnet/`, `gnet_v2/` | `github.com/panjf2000/gnet` v1.6.7 và v2.5.9 |
| `evio/` | `github.com/tidwall/evio` |

Module này có `go.mod` riêng nên các thư viện trên không bị kéo vào người dùng `anet`.

## Các chế độ test

Mỗi server hỗ trợ ba chế độ (`-mode`):

1. **echo**: client gửi một request, server xử lý rồi trả lại nguyên nội dung. Mô phỏng một RPC có handler.
2. **idle**: giữ nhiều connection nhàn rỗi, đo chi phí của connection không làm gì.
3. **mux**: nhiều request chạy song song trên cùng một connection (multiplexing).

Handler chạy bất đồng bộ qua goroutine pool (không xử lý ngay trong event-loop), vì handler của RPC thường nặng.

## Cách chạy

```bash
# build tất cả binary vào output/bin
. ./scripts/build.sh

# đo server: client dùng `net`, server là từng thư viện lần lượt
./scripts/benchmark_server.sh 1     # 1 = echo, 2 = idle, 3 = mux

# đo client: server dùng `net`, client là từng thư viện
./scripts/benchmark_client.sh 1
```

Script ghim server vào CPU `0..k-1` và client vào các CPU còn lại (`taskset` hoặc `numactl`). Cần máy có ít nhất 4 CPU. Chạy thủ công một server:

```bash
taskset -c 0-3 ./output/bin/anet_reciever -addr=127.0.0.1:7005 -mode=1
taskset -c 4-15 ./output/bin/net_bencher -name=anet -addr=127.0.0.1:7005 -mode=1 -b=1024 -c=500 -n=1500000
```

`-b` là kích thước payload (byte), `-c` là số connection đồng thời, `-n` là tổng số request. Để vẽ biểu đồ từ log, dùng `scripts/update_data.sh` (cần `matplotlib`).

## Kết quả mẫu

Echo, payload 1 KB, 1,5 triệu request mỗi lượt, 0 request lỗi, trung bình 2 lượt.
Server ghim 4 CPU, client ghim 12 CPU, cùng một máy 16 CPU (Linux, Go 1.27.4).

**TPS (request/giây, càng cao càng tốt)**

| Số connection | anet | netpoll v0.7.5 | net | evio | gnet v1 | gnet v2 |
|---|---|---|---|---|---|---|
| 100 | 416k | 413k | 415k | 353k | 333k | 324k |
| 500 | 386k | 343k | 406k | 250k | 259k | 283k |
| 1000 | 320k | 336k | 350k | 260k | 274k | 273k |

**TP99 (ms, càng thấp càng tốt)**

| Số connection | anet | netpoll v0.7.5 | net | evio | gnet v1 | gnet v2 |
|---|---|---|---|---|---|---|
| 100 | 0,72 | 0,77 | 1,38 | 0,94 | 1,23 | 1,29 |
| 500 | 3,27 | 3,89 | 5,10 | 5,35 | 5,19 | 4,53 |
| 1000 | 6,73 | 5,92 | 8,08 | 8,04 | 7,72 | 7,75 |

### Cách đọc kết quả

- `anet` và `netpoll` gần như ngang nhau, đúng kỳ vọng vì anet dẫn xuất từ netpoll. Chênh lệch giữa hai bên nằm trong mức nhiễu của phép đo.
- `anet` có TP99 thấp nhất ở 100 và 500 connection. Ở 1000 connection, `netpoll` thấp hơn.
- **`net` chuẩn của Go cho TPS cao nhất ở 500 và 1000 connection.** Với server chỉ 4 CPU và handler nhẹ, mô hình goroutine-per-connection vẫn rất mạnh.
- Đây là kiểm tra không bị thoái lui, không phải số liệu tuyệt đối. Mỗi mức chỉ có 2 mẫu, client và server chạy cùng một máy, máy có thể đang chạy tác vụ khác. Hãy tự chạy lại trên môi trường của bạn trước khi kết luận.

## Kết quả chế độ idle

Chế độ `-mode=2` mở thêm `4 × c` connection nhàn rỗi (80%) trước khi chạy echo với `c` connection hoạt động. Dưới đây là TPS và **RSS cao nhất của tiến trình server** (đo qua `/proc`), 1 lượt mỗi ô, nên nhiễu còn lớn hơn bảng echo.

| Số connection hoạt động / nhàn rỗi | | anet | netpoll v0.7.5 | net | evio | gnet v1 | gnet v2 |
|---|---|---|---|---|---|---|---|
| 500 / 2000 | TPS | 263k | 389k | 393k | 215k | 286k | 237k |
| | RSS (MB) | 36 | 36 | 31 | 12 | 17 | 16 |
| 1000 / 4000 | TPS | 323k | 324k | 346k | 208k | 267k | 274k |
| | RSS (MB) | **107** | **107** | 50 | 18 | 23 | 22 |

> Bảng trên đo **trước** khi `anet` cấp phát buffer chậm (xem bên dưới). Số liệu `anet` hiện tại tốt hơn ở cột RSS.

### Cấp phát buffer chậm cho connection

Heap profile (`pprof`, 4000 connection nhàn rỗi, chưa có tải) cho thấy `LinkBuffer` đầu vào chiếm khoảng 62% heap: mỗi connection cấp phát sẵn 8 KB trong `connection.init`. `anet` nay khởi tạo buffer rỗng và cấp phát ở lần đọc đầu tiên.

| Heap in-use, 4000 connection nhàn rỗi | |
|---|---|
| anet trước | 46 MB |
| anet sau | 13 MB |
| net chuẩn Go (4006 goroutine) | 21 MB |

A/B trên tải thật (trung bình 3 lượt, xen kẽ trước/sau, 0 request lỗi):

| Bài test | RSS trước | RSS sau | TPS trước | TPS sau | TP99 trước | TP99 sau |
|---|---|---|---|---|---|---|
| idle 1000 + 4000 | 93 MB | **66 MB** | 307k | 305k | 7,01 ms | 7,48 ms |
| idle 500 + 2000 | 44 MB | **36 MB** | 366k | 358k | 3,48 ms | 3,64 ms |
| echo 500 | 33 MB | 32 MB | 386k | 366k | 3,19 ms | 3,51 ms |
| echo 100 | 15 MB | 13 MB | 371k | 419k | 0,89 ms | 0,70 ms |

### Cách đọc

- Trước khi tối ưu, ở 4000 connection nhàn rỗi `anet` và `netpoll` dùng gấp đôi bộ nhớ của `net` chuẩn (107 MB so với 50 MB). Giả thuyết "poller tiết kiệm bộ nhớ hơn goroutine-per-connection" không đúng trong bài này.
- Sau khi cấp phát chậm, RSS của `anet` giảm 18 đến 29% ở bài idle, nhưng vẫn còn cao hơn `net` (66 MB so với 50 MB) và `gnet`/`evio` (khoảng 20 MB). Phần còn lại cần xử lý thêm, như hai channel mỗi connection và `LinkBuffer` đầu ra.
- TPS và TP99 lệch cả hai chiều trong phạm vi 3 đến 5%, trong mức nhiễu của phép đo. Với 3 mẫu mỗi ô chưa loại trừ được mức chậm khoảng 5%.
- Lượt `anet` 500/2000 ở bảng idle phía trên (263k) thấp hơn `netpoll` (389k), nhưng ở A/B 3 lượt `anet` đạt 366k, nên 263k nhiều khả năng là nhiễu của một mẫu.
- RSS là tổng của tiến trình, gồm cả bộ nhớ runtime Go và buffer. Nó chưa tách riêng chi phí của connection.

## Thử nghiệm io_uring

`iouring/` (build tag `iouring`) và `epollraw/` là hai server echo thử nghiệm có **cùng cấu trúc**: mỗi CPU một event loop, mỗi loop một listener `SO_REUSEPORT`, handler chạy ngay trong loop. Chúng chỉ khác nhau ở cơ chế I/O: `epollraw` dùng epoll cộng `read`/`write`, còn `iouring` dùng io_uring (completion). Mục đích là tách riêng phần lợi ích của io_uring khỏi phần lợi ích của kiến trúc.

```bash
go build -tags iouring -ldflags=-checklinkname=0 -o output/bin/iouring_reciever ./iouring
go build -o output/bin/epollraw_reciever ./epollraw
```

`-checklinkname=0` cần vì thư viện `giouring` dùng `go:linkname` tới `syscall.munmap`, mà Go 1.23+ chặn mặc định.

Echo 1 KB (trừ dòng ghi rõ), server 4 CPU, trung bình 3 lượt, 0 request lỗi:

| Bài test | anet | epoll thuần | io_uring |
|---|---|---|---|
| c=100, TPS | 409k | 473k | **502k** |
| c=500, TPS | 346k | 470k | **499k** |
| c=1000 (idle 4000), TPS | 324k | 410k | **418k** |
| c=100, 16 KB, TPS | 92k | 131k | **134k** |
| c=1000 (idle 4000), RSS | 63 MB | **39 MB** | 48 MB |

### Cách đọc

- io_uring chỉ nhanh hơn epoll thuần khoảng **2 đến 6%** (502k so với 473k, 418k so với 410k). Phần chênh lệch lớn với `anet` (15 đến 35%) đến từ **kiến trúc**: xử lý handler ngay trong event loop, và mỗi loop có listener riêng thay vì một listener chia cho nhiều poller. `anet` thì chuyển mỗi request sang một goroutine, đổi lấy việc handler nặng không chặn poller.
- Hai server thử nghiệm không có goroutine pool, không có timeout, không có callback vòng đời, không có Reader/Writer nocopy. Vì vậy so sánh này cho biết **trần hiệu năng** của một thiết kế tối giản, không phải so sánh tính năng ngang bằng.
- Cho nên tích hợp io_uring vào `anet` chỉ hứa hẹn thêm vài phần trăm, trong khi cái giá là rủi ro an toàn bộ nhớ (kernel ghi trực tiếp vào buffer của Go, phải giữ buffer sống đến khi nhận CQE) và phụ thuộc vào kernel mới. Hướng đáng đầu tư hơn là giảm chi phí kiến trúc của `anet` (chế độ xử lý trong loop cho handler nhanh, listener `SO_REUSEPORT` cho mỗi poller).

## Chế độ inline (`WithInlineRequest`)

Chế độ inline chạy `OnRequest` ngay trên poller thay vì chuyển sang goroutine, và **là mặc định** của `anet.NewEventLoop` (trừ khi có `WithOnConnect`; `WithGoroutineRequest()` tắt hẳn). Trong benchmark, `ANET_INLINE=0` chọn chế độ goroutine để so sánh, số poller đặt bằng `ANET_LOOPS=<n>` (xem `anet/main.go`). Handler không được chặn: `Reader` trả `ErrNotEnoughData` thay vì chờ dữ liệu, và codec của benchmark dùng `Peek` trước khi tiêu thụ.

Server 4 CPU, echo, trung bình 3 lượt, 0 request lỗi (`inl2`/`inl4` là inline với 2/4 poller):

| Bài test | thường (1 poller) | inl2 | inl4 | epoll thuần |
|---|---|---|---|---|
| c=100, 1 KB, TPS | 371k | 244k | **389k** | 434k |
| c=500, 1 KB, TPS | 330k | 239k | **393k** | 391k |
| c=1000 (idle 4000), TPS | 312k | 217k | **322k** | 361k |
| c=100, 16 KB, TPS | 84k | 97k | **120k** | 137k |
| c=500, 1 KB, TP99 (ms) | 3,77 | 5,40 | **3,62** | 4,47 |
| c=1000 (idle 4000), RSS | 66 MB | 49 MB | 57 MB | 45 MB |

### Cách đọc

- Với **số poller bằng số CPU** (`inl4`), chế độ inline nhanh hơn chế độ thường khoảng 0% đến 19% với payload nhỏ (khoảng chênh giữa các lần đo lặp lại) và 43% đến 67% với payload 16 KB, và ở c=500 có lần đạt ngang epoll thuần. RSS thấp hơn vì không tạo goroutine cho mỗi request. Lợi ích rõ nhất ở payload lớn, nơi chi phí chuyển goroutine và đánh thức thread chiếm tỉ lệ lớn.
- `WithInlineRequest` tự đặt số poller bằng `runtime.GOMAXPROCS(0)` nếu bạn chưa tự đặt (kiểm tra: server pinned 4 CPU ra 4 poller). Nên không cần đặt `ANET_LOOPS` khi dùng inline. Một lượt đo lặp lại cho thấy `auto` và `inl4` đặt tay cùng mức trong sai số (chênh 8% đến 11% ở payload nhỏ là nhiễu giữa các lần đo, cấu hình giống hệt).
- Với **ít poller hơn số CPU** (mặc định 1, hoặc `inl2`), chế độ inline **chậm hơn rõ rệt** (đến 40%). Lý do: toàn bộ handler và `sendmsg` (chiếm khoảng 66% CPU trong profile) dồn vào số thread ít hơn. Vì vậy phải đặt `SetNumLoops` bằng số CPU khi bật inline.
- Số poller **không** có tác dụng ở chế độ thường: 1, 2 hay 4 poller cho kết quả ngang nhau trong mức nhiễu, vì handler chạy trên goroutine và Go tự trải ra các core.
- Chỉ dùng cho handler nhẹ và không chờ I/O. Một handler chặn sẽ chặn cả poller và mọi connection trên nó.

## Độ trễ khi mạng chậm (`lag/`)

`lag/main.go` mô phỏng một luồng khung 1000 byte mỗi 2ms (4 Mbps) đi qua đường truyền giới hạn 2 Mbps, rồi đo **tuổi của khung khi tới người nhận**. Chạy trong network namespace với `tc netem`:

```sh
unshare -rn sh -c 'ip link set lo up; tc qdisc add dev lo root netem rate 2mbit delay 10ms limit 10;
  ./lag -mode=default -d=6s; ./lag -mode=lowat -lowat=4096 -pending=8192 -d=6s'
```

Kết quả (một lượt, `limit 10` gói ở hàng đợi qdisc; tee dùng `DropOldest`):

| Cấu hình | Tuổi khung p50 | p99 |
|---|---|---|
| mặc định (buffer kernel tự chỉnh, `MaxPending` 64 MiB) | 1.7 s | 3.4 s |
| `WithNotSentLowat(16K)`, `MaxPending` 32K | 293 ms | 448 ms |
| `WithNotSentLowat(4K)`, `MaxPending` 8K | 212 ms | 351 ms |

- Hàng đợi trong kernel và trong tee là nguồn gây trễ chính: giữ chúng ngắn giảm trễ khoảng 6 đến 8 lần, đổi lại tee bỏ khoảng một nửa số khung (đúng bằng phần mạng không chở nổi).
- Mức khoảng 200 ms còn lại không đến từ anet: hàng đợi qdisc nhỏ làm rớt gói và TCP phải chờ RTO tối thiểu 200 ms để gửi lại. Ở mức này `MaxAge` không cải thiện thêm trong thử nghiệm này.
- Đây là mô phỏng trên loopback với một cấu hình netem, chưa phải mạng thật.

### Throughput có bị mất khi dùng `WithNotSentLowat` không (`bulk/`)

`bulk/main.go` gửi một khối dữ liệu lớn qua một kết nối anet và đo Mbit/s. Đường 20 Mbit, trễ một chiều 10 ms (RTT 20 ms), 12 MB, 3 lượt mỗi cấu hình:

| Cấu hình | Mbit/s (3 lượt) | Trung bình |
|---|---|---|
| mặc định | 12.5, 13.4, 11.2 | 12.4 |
| `lowat` 16 KB | 12.3, 11.3, 12.9 | 12.2 |
| `lowat` 128 KB | 13.8, 12.8, 10.3 | 12.3 |

Các cấu hình ngang nhau trong khoảng nhiễu giữa các lượt, nên ở mức này `lowat` không làm giảm thông lượng. Với đường 100 Mbit và RTT 100 ms (có hoặc không mất gói), kết quả dao động rất mạnh (3 đến 25 Mbit/s) do thuật toán tránh nghẽn của TCP và netem chi phối, nên các lượt đó **không kết luận được** gì về `lowat`. Vì vậy `lowat` vẫn là tuỳ chọn, không đặt mặc định.

## Kết nối "dùng rồi nghỉ" (`usedidle/`)

Phần lớn connection của một server thật đã gửi vài request rồi nằm chờ. `usedidle/main.go` mở 5000 connection,
mỗi connection gửi 3 request echo 1 KB, rồi giữ nguyên; RSS của server đo qua `/proc` khi tất cả đã nghỉ.

Heap profile ban đầu cho thấy connection nghỉ vẫn giữ buffer đọc (8 KB), buffer ghi và `Barrier` của `Flush`.
`anet` nay trả buffer về pool chung khi đã đọc/ghi hết (`dropIfDrained`) và chỉ mượn `Barrier` trong lúc gửi.

| Server (5000 connection nghỉ) | RSS, 3 lượt | Trung bình | Mỗi connection (trừ RSS lúc khởi động) |
|---|---|---|---|
| anet trước | 78,4 / 77,4 / 76,6 MB | 77,5 MB | 13,4 KB |
| anet, bước 1: trả buffer đọc | 56,2 / 55,3 / 52,1 MB | 54,5 MB | 8,7 KB |
| **anet, bước 2: + buffer ghi, Barrier** | 24,2 / 25,2 MB (2 lượt) | **24,7 MB** | **2,7 KB** |
| netpoll v0.7.5 | 71,3 / 71,0 / 70,8 MB | 71,0 MB | |
| net chuẩn Go | 75,1 / 76,5 / 74,7 MB | 75,4 MB | |
| gnet v2 | 24,5 / 24,8 MB (2 lượt cùng đợt bước 2) | 24,7 MB | 3,3 KB |

Tốc độ không đổi (echo 1 KB, 3 lượt xen kẽ trước/sau, server 4 CPU):

| | TPS trước | TPS sau | TP99 trước | TP99 sau | RSS server khi tải trước | sau |
|---|---|---|---|---|---|---|
| c=1000 | 494k / 492k / 487k | 497k / 497k / 508k | 5,7–6,0 ms | 4,8–5,6 ms | 40,5 MB | **19,4 MB** |
| c=100 | 642k / 658k / 399k | 651k / 382k / 636k | 0,8 ms | 0,8 ms | 17,4 MB | 16,1 MB |

- Ở c=100 mỗi bên có một lượt rơi xuống khoảng 390k: máy dùng chung, coi là nhiễu; trung vị hai bên ngang nhau.
- Buffer chỉ được trả khi handler đã đọc hết và đã `Release` (hoặc chỉ dùng hàm đọc có copy như `ReadString`).
  Handler giữ slice từ `Next`/`Peek` mà không `Release` vẫn giữ buffer như trước, nên dữ liệu đó luôn hợp lệ.
- Chỉ áp dụng cho chế độ inline (mặc định); ở chế độ `WithGoroutineRequest` buffer đọc giữ nguyên như cũ.
