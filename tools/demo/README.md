# Bộ quay demo

Quay clip và ảnh chụp của Agent Desk cho trang giới thiệu (khoabug.com/agent-desk). App chạy thật, nhưng trên dữ liệu mẫu và một Claude Code giả lập, nên không đọc gì trong máy và không tốn token.

Đây là một module Go riêng (`go.mod` ở thư mục này), nên `go build ./...` và `go test ./...` ở gốc repo bỏ qua nó. Không file nào ở đây vào bản build của app.

## Chạy

Cần Windows, Go, Node.js và Wails như khi build app (xem README ở gốc repo), và một ký tự ổ đĩa còn trống (mặc định `R:`).

```
.\tools\demo\run-demo.ps1
```

Mất khoảng hai phút. Cửa sổ app hiện lên và tự chạy theo kịch bản, không cần đụng chuột. Mọi thứ nằm trong `%TEMP%\agent-desk-demo`, không ghi gì vào repo:

```
src\        bản sao repo, có cài cửa test, build ra build\bin\agent-desk-e2e.exe
root\       thư mục mẫu, được subst thành ổ R: lúc quay
take\       các frame quay được
out\        watch.gif, work.gif, shots\ (ảnh chụp từng lúc)
out\page\   đủ bộ ảnh của trang: chép cả thư mục này vào my-labs\public\img\agent-desk\
```

Các cờ:

```
-SkipBuild   dùng lại bản build lần trước (khi chỉ sửa kịch bản)
-OnlyGif     không quay lại, chỉ làm lại clip và ảnh từ frame đã có
-Work <dir>  đổi thư mục làm việc
-Drive Q:    đổi ký tự ổ đĩa
-Scale 0.75  cỡ clip so với cửa sổ
```

## Các phần

```
run-demo.ps1     làm hết: build, dựng dữ liệu mẫu, quay, làm clip và ảnh, dọn dẹp
demo.mjs         dữ liệu mẫu (project, session, agent, workflow, một tháng lịch sử)
                 và kịch bản điều khiển app trong lúc quay
post.mjs         tách bản quay thành hai clip (xem / làm việc) và lấy ảnh chụp từng lúc
clip/            ghi cửa sổ app thành frame (PrintWindow), rồi ghép thành GIF có vẽ con trỏ
democlaude/      Claude Code giả lập, đóng một session theo kịch bản cho phần chat trong app
pageimg/         từ ảnh chụp ra bộ ảnh của trang: ảnh nguyên tấm, các phần cắt cho thẻ, icon 128px
e2e/e2e_hook.go  cửa test để kịch bản điều khiển trang của app
```

## An toàn

- Cửa test chỉ được build với tag `e2e`, và chỉ trong bản sao ở `%TEMP%`. `run-demo.ps1` chèn lời gọi `a.startE2E(ctx)` vào `app.go` của bản sao, không sửa repo.
- Cửa test chỉ nghe ở `127.0.0.1`, chỉ khi có cả cổng lẫn token trong biến môi trường, và token sinh mới mỗi lần chạy.
- App chạy với `CLAUDE_CONFIG_DIR`, `APPDATA` và `LOCALAPPDATA` trỏ vào ổ mẫu, nên không thấy transcript thật và không đụng `history.json` thật. Các biến `CLAUDE*` / `ANTHROPIC*` chỉ đặt trong phiên shell hiện tại bị gỡ trong lúc quay.
- Hộp thoại chọn thư mục của Windows không được quay, vì nó hiện thư mục thật của máy. Kịch bản hỏi thẳng câu xác nhận tin tưởng thư mục.
- Xong việc, script đóng app, dừng các tiến trình giả, gỡ ổ subst, và xoá đăng ký thông báo Windows mà bản test tạo ra.

## Khi giao diện app đổi

- Kịch bản trong `demo.mjs` bấm vào phần tử theo CSS selector (`.bay`, `li.strip[data-open-trail=…]`, `.wf-toggle`, `.permit`…). Đổi tên class hay thuộc tính thì sửa ở đó.
- `pageimg/main.go` cắt các phần cho thẻ ở toạ độ cố định trên cửa sổ 1266×793. Bố cục bảng đổi thì xem lại ảnh trong `out\shots\` và dời các hình chữ nhật.
- `democlaude` nói giao thức JSON của Claude Code 2.1.322. Nếu app đổi cách nói chuyện với Claude Code thì phải sửa cả hai bên.
