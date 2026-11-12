# Agent Desk

Một cửa sổ cho thấy mọi session Claude Code đang chạy trên máy: mỗi session đang làm gì, đã gọi những subagent nào, và tất cả đã tiêu bao nhiêu token, quy ra bao nhiêu tiền. Không cần hỏi từng agent "làm tới đâu rồi".

Phần theo dõi chỉ đọc: app đọc file mà Claude Code đã ghi sẵn trên đĩa, không ghi gì vào thư mục của Claude Code và không đọc file đăng nhập. Session đang chạy trong VS Code chỉ được xem, không được ra lệnh từ đây: mỗi cuộc trò chuyện có một chỗ, và chỗ của chúng là VS Code.

Ngoài ra app tự mở được session Claude Code của riêng nó tại một project và cho bạn chat ngay trong cửa sổ này, xem mục [Mở session và chat trong app](#mở-session-và-chat-trong-app).

## Dùng

Chạy `monitor-agent.exe`. App từng có tên Agent Monitor; file chạy và thư mục dữ liệu vẫn giữ tên `monitor-agent` để bảng giá tự chỉnh và danh sách project của bản cũ dùng tiếp được. Lần mở đầu mất vài giây vì phải đọc toàn bộ transcript; sau đó app tự quét mỗi 2 giây và chỉ đọc phần mới.

Đầu trang là một khối tổng quan:

- **Dãy bàn làm việc** (bên trái): mỗi project một bàn, mỗi người ngồi là một việc đang diễn ra, gồm luồng chính của session và từng agent chưa xong. Người đang chạy có ba chấm nhấp nháy trên đầu và đèn laptop xanh; đang chờ thì có dấu tạm dừng màu hổ phách; session rảnh vẽ bằng nét viền. Dưới mỗi bàn là chi phí của project. Bấm vào một bàn để cuộn tới phần của project đó.
- **Quầy chi phí** (bên phải): tổng chi phí hôm nay và tổng từ đầu các session đang mở, kèm chi phí theo từng model.
- **Chi phí theo loại token:** thanh này vẽ theo tiền chứ không theo số token, vì phần lớn token là cache đọc nhưng tiền lại chia khá đều cho cache đọc, cache ghi và output.
- **Nút "Chi tiết theo project"** mở các bảng đầy đủ: token và chi phí theo project, theo loại token, theo model.

Bên dưới là bảng theo dõi:

- **Mỗi project** liệt kê các session của nó, project đang có hoạt động đứng trước. Dòng đầu mỗi khối là session (luồng chính), các dòng dưới là subagent, cùng chung một bộ cột. Dòng tiêu đề cột dính ở đầu khi cuộn.
- **Cột nhịp 30 phút** vẽ số sự kiện mỗi phút trên cùng một trục thời gian cho mọi dòng, nên so được agent nào dừng lúc nào.
- **Bấm vào một dòng** (luồng chính của session hoặc một subagent) để mở khung bên phải với các bước gần đây của agent đó: nhiệm vụ được giao, từng lần gọi tool kèm kết quả, những đoạn nó viết, báo cáo cuối. Khung tự cập nhật theo transcript và chỉ để xem. Ở đây câu chữ dài được cắt bớt, chuỗi trông như mật khẩu được che, và công tắc **Ẩn nội dung** giấu hết phần chữ, chỉ để lại tên tool. Bấm lại dòng đó để đóng.
- **Bấm vào tên project** để chuyển sang cửa sổ VS Code đang mở project đó. Lần đầu VS Code có thể hỏi xác nhận việc mở thư mục từ ứng dụng ngoài.

Nếu Windows đang bật "giảm chuyển động" thì hoạt cảnh đứng yên.

Ba công tắc ở góc trên:

| Công tắc | Tác dụng |
|---|---|
| **Báo khi xong** | Hiện thông báo của Windows khi một session xong lượt (đang chờ bạn) hoặc một agent làm xong. Vẫn báo khi cửa sổ đang thu nhỏ. Bấm vào thông báo để mở lại app. |
| **Ẩn agent đã kết thúc** | Chỉ để lại agent đang chạy hoặc im lặng. |
| **Ẩn nội dung** | Giấu câu lệnh, yêu cầu và kết quả, dùng khi chia sẻ màn hình. Thông báo cũng bỏ tên session và tên agent. |

Trạng thái của agent:

| Trạng thái | Nghĩa |
|---|---|
| Đang chạy | Vừa ghi transcript trong 2 phút gần đây. |
| Chờ lệnh | Đã gọi một tool và chưa có kết quả. Build hay test chạy lâu là bình thường; quá 3 giờ thì coi như đã dừng. |
| Im lặng | 2 đến 15 phút không ghi gì và không chờ tool nào: model đang suy nghĩ lâu, hoặc đã treo. |
| Đã dừng | Im lặng quá lâu mà không kết thúc, nhiều khả năng bị ngắt giữa chừng. |
| Xong | Đã trả kết quả, hoặc được Claude Code đánh dấu xong. |

Hai cột token dễ nhầm:

| Cột | Ý nghĩa |
|---|---|
| **Context** | Kích thước context ở lần gọi API gần nhất. Đây là con số Claude Code hiển thị trong Agent map. |
| **Đã tiêu** | Tổng token đã xử lý qua mọi lần gọi. Mỗi lần gọi gửi lại toàn bộ context, nên số này thường lớn gấp hàng chục lần Context. Chi phí tính theo số này. |

## Mở session và chat trong app

Bảng theo dõi chỉ thấy project nào đang có session Claude Code chạy. Để làm việc với một project chưa mở ở đâu cả, thêm nó vào app rồi mở session tại đó.

**Thêm project.** Bấm **Thêm project** (bàn trống có dấu cộng ở cuối dãy bàn) và chọn thư mục. App hỏi một lần xem bạn có tin thư mục đó không, vì Claude Code do một chương trình khác khởi chạy sẽ không tự hỏi câu này: hook, MCP server và các quy tắc cấp quyền khai báo trong thư mục có hiệu lực ngay, kể cả quy tắc cho chạy lệnh mà không hỏi. Chỉ đồng ý với thư mục bạn tin tưởng. Project đã thêm luôn hiện trên bảng dù chưa có session; **Bỏ khỏi danh sách** gỡ nó ra mà không đụng tới thư mục. Thư mục mạng dạng `\\máy\thư-mục` chưa được hỗ trợ.

**Mở session.** Bấm **Session mới** ở đầu khối của project. App chỉ mở session ở thư mục bạn đã xác nhận tin tưởng: với project có trên bảng chỉ vì đang có session chạy ở đó, app hỏi câu ở trên trước, một lần, và đồng ý thì project được thêm vào danh sách. Khung bên phải hiện thư mục session sẽ chạy và file Claude Code sẽ được dùng. Chọn quyền của agent, nhập yêu cầu đầu tiên rồi Enter (Shift+Enter để xuống dòng).

| Quyền | Agent được làm gì |
|---|---|
| **Hỏi trước khi sửa file hay chạy lệnh** (mặc định) | Mỗi thao tác có thay đổi đều chờ bạn bấm **Cho phép**. |
| **Tự sửa file, hỏi trước khi chạy lệnh** | Sửa file trong project không hỏi lại; chạy lệnh vẫn chờ bạn. Thế nào là "sửa file" do Claude Code quyết định. |
| **Chỉ đọc và lập kế hoạch** | Agent đọc code rồi đề xuất kế hoạch, không sửa gì. Nếu agent xin thoát chế độ này và bạn cho phép, từ đó nó làm việc như chế độ hỏi trước. |

App không có chế độ "không bao giờ hỏi", và luôn truyền rõ chế độ bạn chọn cho Claude Code. Nếu Claude Code báo nó khởi động ở chế độ khác, do cấu hình của project hay của máy ghi đè, app dừng session đó ngay. Mỗi session mới quay về chế độ hỏi trước; quyền rộng hơn phải chọn lại từng lần.

Có một thứ app không thay bạn kiểm soát được: quy tắc `permissions.allow` và hook đã có sẵn trong `~/.claude` hoặc trong project vẫn cho một số tool chạy mà không hiện thẻ xin phép, y như khi dùng Claude Code trong terminal. Câu hỏi tin tưởng ở trên là để nhắc điều đó.

**Khi agent xin phép,** một thẻ màu hổ phách hiện ngay trên ô nhập và agent đứng chờ cho tới khi bạn bấm **Cho phép** hoặc **Từ chối**. Thẻ được làm theo một nguyên tắc: thứ bạn duyệt là thứ bạn đã thấy.

- Thẻ ghi tên thật của tool.
- Lệnh sửa hoặc ghi file hiện thành diff đỏ/xanh, đủ mọi dòng cũ và mới.
- Với tool khác, thẻ hiện từng trường của yêu cầu, kể cả trường app không biết tên (ví dụ `run_in_background`, `timeout`). Lời agent tự mô tả về lệnh được ghi rõ là lời của agent và đặt sau chính câu lệnh.
- Ký tự không nhìn thấy được (ký tự điều khiển, ký tự rộng bằng không, ký tự đảo chiều chữ) được thay bằng dấu hiện rõ như `␍` hay `⟨U+200B⟩`, và thẻ có dòng cảnh báo.
- Nội dung dài thì phải cuộn hết mới bấm được **Cho phép**. Dài tới mức không hiện hết (trên 200 nghìn ký tự) thì thẻ nói rõ còn thiếu bao nhiêu.
- Hai nút chỉ bấm được sau khi thẻ đứng yên khoảng nửa giây, để một cú bấm nhắm chỗ khác không rơi nhầm vào chúng.

Yêu cầu nào app không đọc được thì app tự từ chối và ghi lại trong hội thoại. Nếu đang bật **Báo khi xong**, Windows cũng báo cho bạn biết có session đang chờ duyệt, và trên bảng dòng của session đó chuyển sang **Chờ bạn cho phép**.

**Trong lúc agent làm,** câu trả lời hiện dần theo từng chữ, và dòng cuối khung cho biết nó đang làm gì: đang suy nghĩ, đang viết, đang chạy tool nào. Mỗi lần gọi tool là một dòng, bấm vào để xem đầu vào và kết quả; lần sửa file thì mở sẵn dưới dạng diff. **Dừng lượt này** ngắt lượt đang chạy và giữ session lại để hỏi tiếp. **Kết thúc session** đóng session. **Đóng** (hoặc Esc) chỉ cất khung đi: session vẫn chạy, bấm vào dòng của nó trên bảng (hoặc nút **Mở chat**) để quay lại. Khi có nhiều session, ô chọn ở đầu khung chuyển qua lại giữa chúng.

Vài điều nên biết trước:

- **Session sống cùng app.** Đóng app là kết thúc mọi session app đã mở, vì không còn ai trả lời yêu cầu xin phép của chúng. Lượt đang chạy được yêu cầu dừng trước, rồi mọi thứ session đã khởi chạy (lệnh đang chạy, MCP server, tiến trình nền) bị tắt theo. Trên Windows điều này đúng cả khi app bị tắt đột ngột. Session đã kết thúc hiện chưa mở lại được từ app.
- **Chi phí mở session.** Mỗi session mới nạp lại system prompt, khoảng 30 nghìn token, giống hệt khi mở Claude Code trong VS Code. Quy theo giá API là khoảng 0,5 USD với Fable 5.2 và 0,1 USD với Sonnet 5.5. Chat tiếp trong session đang mở rẻ hơn nhiều so với mở session mới cho từng câu hỏi.
- **Model và cấu hình** là của Claude Code trên máy: session dùng model mặc định, hook, MCP server và quyền đã cấp sẵn trong `~/.claude` và trong project, y như một session mở từ terminal.
- **Khung chat không bị che** bởi công tắc **Ẩn nội dung**. Trước khi chia sẻ màn hình, hãy đóng khung chat. (Khung xem bước của agent chạy ngoài app thì có theo công tắc này.)

**App tìm Claude Code ở đâu.** Theo thứ tự: biến môi trường `MONITOR_AGENT_CLAUDE` (đường dẫn đầy đủ tới file `claude`), lệnh `claude` trong `PATH`, bản cài riêng ở `~/.local/bin`, rồi bản đi kèm extension Claude Code của VS Code và các trình soạn thảo cùng họ. Máy nào dùng được Claude Code trong VS Code thì không cần cài gì thêm. File được chọn hiện ngay trong khung **Session mới** trước khi bạn gửi yêu cầu đầu tiên; app không kiểm tra chữ ký số của file đó.

## Chi phí được tính thế nào

Chi phí là **quy đổi theo giá API niêm yết**, tính riêng cho từng loại token (input, output, cache đọc, cache ghi 5 phút, cache ghi 1 giờ) và từng model. Nếu bạn dùng gói subscription, đây không phải số tiền thực trả.

Bảng giá mặc định nằm trong [internal/pricing/pricing.json](internal/pricing/pricing.json). Các model đánh dấu `verified` cho ra đúng từng số lẻ so với chi phí Claude Code tự ghi lại; các model khác dùng giá input/output công bố với giá cache-đọc giả định, và trang sẽ ghi chú "ước tính".

Để sửa giá hoặc thêm model, tạo file sau:

- Windows: `%AppData%\monitor-agent\pricing.json`
- macOS: `~/Library/Application Support/monitor-agent/pricing.json`
- Linux: `~/.config/monitor-agent/pricing.json`

```json
{
  "models": {
    "claude-opus-5-5": { "input": 4, "output": 20, "cacheRead": 0.20 }
  }
}
```

Giá tính bằng USD cho một triệu token. Chỉ cần ghi model muốn đổi; phần còn lại giữ theo bảng mặc định. Model không có trong bảng vẫn được đếm token nhưng không tính vào chi phí, và trang sẽ báo.

## Quyền riêng tư và an toàn

Transcript chứa source code, câu lệnh, và đôi khi cả mật khẩu hay token từng hiện trong terminal. App xử lý chúng như sau:

- **App không gửi gì đi đâu.** Bản build theo `build.ps1` không mở kết nối mạng nào. Trang giao diện chạy dưới chính sách chặn mọi request ra ngoài và mọi script không thuộc app. Session do app mở là một tiến trình Claude Code bình thường: nó nói chuyện với Anthropic bằng tài khoản Claude Code của bạn, như mọi session khác.
- **Bảng theo dõi chỉ hiện đoạn trích ngắn** (tối đa 180 ký tự) của yêu cầu, câu lệnh và kết quả; không giữ transcript trong bộ nhớ.
- **Che chuỗi trông như thông tin đăng nhập** trước khi hiện trên bảng: `PASSWORD=…`, header `Authorization`, token trong URL, khoá AWS/GitHub, JWT, private key. Đây là so khớp theo mẫu nên không bắt được mọi trường hợp; khi chia sẻ màn hình hãy bật **Ẩn nội dung**.
- **Khung xem bước của một agent** hiện nhiều hơn bảng (vài dòng cho mỗi đoạn văn và mỗi kết quả tool, khoảng 120 bước gần nhất) nhưng vẫn qua cùng bộ che ở trên, và theo công tắc **Ẩn nội dung**.
- **Khung chat không che gì.** Thẻ xin phép hiện đủ nội dung của yêu cầu, vì thứ bạn duyệt phải là thứ bạn đã thấy; hội thoại hiện câu chữ của agent như nó viết, chỉ cắt bớt đầu vào và kết quả tool quá dài. App giữ tối đa 600 mục gần nhất của mỗi cuộc trò chuyện trong bộ nhớ và không ghi chúng ra đĩa; bản ghi đầy đủ là transcript do chính Claude Code lưu.
- **File app tạo ra:** hồ sơ WebView2 trong `%LocalAppData%\monitor-agent`; danh sách project đã thêm trong `%AppData%\monitor-agent\projects.json`; và khi bật thông báo thì một mục đăng ký thông báo trong registry của user hiện tại. App không tự ghi gì vào `~/.claude`; session do app mở thì ghi transcript của nó vào đó như mọi session Claude Code.
- **Session chỉ mở được ở thư mục bạn đã xác nhận.** Việc xác nhận là một hộp thoại của hệ điều hành do phần Go hiện ra, không phải của trang giao diện, và phần Go kiểm tra lại danh sách đó mỗi lần mở session. Trang giao diện cũng chỉ gọi tên được project đang hiện trên bảng.

Giới hạn của các lớp bảo vệ này cần nói thẳng: từ khi app mở được session, trang giao diện là bên bấm **Cho phép**. Nếu có ai chạy được mã của họ bên trong trang (điều mà chính sách chặn script lạ và việc không bao giờ dựng HTML từ chuỗi được đặt ra để ngăn), họ có thể mở session ở một thư mục bạn đã tin tưởng và tự duyệt yêu cầu của nó. Ở bản chỉ theo dõi, trường hợp xấu nhất tương ứng chỉ là lộ thứ đang hiện trên màn hình.

App đọc những file sau trong thư mục cấu hình của Claude Code (`~/.claude`, hoặc `CLAUDE_CONFIG_DIR` nếu có đặt):

| Đường dẫn | Dùng để |
|---|---|
| `sessions/<pid>.json` | Biết session nào đang chạy, ở thư mục nào. Mục của process đã thoát bị bỏ qua. |
| `projects/<project>/<session>.jsonl` | Transcript luồng chính: tiêu đề, yêu cầu gần nhất, token từng lần gọi. |
| `projects/<project>/<session>/subagents/**/agent-*.jsonl` và `.meta.json` | Transcript và mô tả của từng subagent, kể cả agent do workflow sinh ra. |

Định dạng các file này là nội bộ của Claude Code và có thể đổi theo phiên bản (app được viết và kiểm với bản 2.1.322). Parser bỏ qua thứ nó không hiểu thay vì báo lỗi, và trang sẽ cảnh báo nếu có lượt gọi không cộng được. Nếu số liệu bỗng thiếu sau khi nâng Claude Code, chỗ cần xem là [internal/parse](internal/parse/parse.go).

## Build từ source

Cần Go (bản nào cũng được, lệnh `go` tự tải đúng toolchain ghi trong `go.mod`), Node.js 18 trở lên (nên dùng bản LTS còn được hỗ trợ) và [Wails v2](https://wails.io) cùng phiên bản với `go.mod`:

```
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.1
```

Build bản để gửi đi:

```
.\build.ps1
```

Script chạy `go vet`, chạy test, rồi `wails build -clean -trimpath -webview2 error`, và in mã SHA-256 của file exe để người nhận đối chiếu. Cờ `-webview2 error` khiến app báo lỗi thay vì tự tải bộ cài WebView2 khi máy thiếu (Windows 11 có sẵn).

Các lệnh khác:

```
go test ./internal/...
go run ./cmd/dump          # in cùng số liệu ra terminal, để đối chiếu
go run ./cmd/agentprobe    # thử giao thức với Claude Code thật (tốn token thật)
wails dev                  # chạy thử, tự nạp lại khi sửa code
```

`wails dev` mặc định **không đọc dữ liệu thật và khoá việc mở session**: máy chủ dev của Wails nhận kết nối từ mọi trang web đang mở trong trình duyệt trên máy, và một trang như vậy gọi được mọi lệnh của app, kể cả mở session rồi tự duyệt yêu cầu của nó. Đặt `CLAUDE_CONFIG_DIR` tới một thư mục mẫu để xem bảng theo dõi (việc mở session vẫn khoá). Chỉ `MONITOR_AGENT_DEV_REAL_DATA=1` mới mở cả dữ liệu thật lẫn việc mở session; dùng khi bạn chấp nhận rủi ro đó. Bản build bằng `build.ps1` không có máy chủ này.

Bản macOS phải build trên máy Mac.

## Cấu trúc

```
internal/scan     tìm session đang chạy và file transcript của chúng
internal/parse    đọc transcript tăng dần, cộng token, lấy hoạt động gần nhất; đọc các bước cuối của một agent
internal/clean    làm sạch và che chuỗi nhạy cảm trước khi hiển thị; làm hiện ký tự ẩn trong nội dung chờ duyệt
internal/fsx      mở file sao cho không cản trở Claude Code đang ghi; kiểm tra thư mục trước khi chạy gì ở đó
internal/usage    kiểu dữ liệu đếm token
internal/pricing  bảng giá và phép tính chi phí (số nguyên, đơn vị 1e-9 USD)
internal/monitor  ghép tất cả thành dữ liệu cho màn hình, phát hiện việc vừa xong
internal/agent    chạy session Claude Code của app: khởi chạy, gửi yêu cầu, chuyển yêu cầu xin phép
internal/projects danh sách project người dùng đã thêm
app.go, main.go   cửa sổ Wails, vòng quét nền, thông báo, các lệnh của khung chat
frontend/         giao diện (TypeScript thuần, không framework); khung chat ở src/chat.ts
cmd/dump          công cụ dòng lệnh để kiểm tra số liệu
cmd/agentprobe    công cụ dòng lệnh để thử giao thức với Claude Code
```

Các package trong `internal/` không phụ thuộc Wails và có test riêng.

## Giới hạn đã biết

- Chỉ tính các session **đang mở**. Session đã đóng không xuất hiện, kể cả phần chi phí hôm nay của nó.
- Khung chat nói chuyện với Claude Code theo cách các Agent SDK chính thức vẫn làm (JSON từng dòng qua stdin/stdout). Luồng tin nhắn có tài liệu; riêng các tin điều khiển dùng cho xin phép và ngắt lượt thì không được công bố như một giao thức cho chương trình ngoài, nên có thể đổi theo phiên bản (đã kiểm với bản 2.1.322). Nếu sau khi nâng Claude Code mà thẻ xin phép không hiện hoặc nút **Dừng lượt này** không ăn, chỗ cần xem là [internal/agent/protocol.go](internal/agent/protocol.go). Một yêu cầu xin phép mà app không đọc được thì bị app từ chối, không bao giờ được trả lời "cho phép".
- Chỉ chat được với session do app mở. Chưa chọn được model cho session mới, và chưa mở lại được session đã kết thúc.
- Việc tắt hết tiến trình con khi app bị tắt đột ngột dựa vào Job Object của Windows. Trên macOS và Linux, app tắt được cả nhóm tiến trình khi kết thúc session bình thường, nhưng không làm được điều đó nếu chính app bị tắt đột ngột.
- App không kiểm tra chữ ký số của file Claude Code mà nó chạy; đường dẫn của file hiện trong khung **Session mới** để bạn tự đối chiếu.
- Khung chat hiện câu trả lời dạng văn bản thường, chỉ nhận ra khối code; không dựng Markdown, bảng hay hình.
- Một agent "Im lặng" không chắc là treo: model đang suy nghĩ lâu cũng không ghi gì vào transcript.
- Con số Context có thể lệch vài trăm token so với Agent map ở một số agent.
- Nút mở VS Code dùng đường dẫn `vscode://`, nên chỉ mở VS Code bản chính thức, không mở Cursor hay các bản khác.
- Trên macOS và Linux, app chỉ kiểm tra pid còn sống, không kiểm tra được pid có bị process khác dùng lại hay không. Thông báo và nút mở VS Code mới được thử trên Windows.
- Giá cho chế độ fast được nhân theo hệ số công bố cho input/output; phần cache ở chế độ này là giả định.
- File exe chưa ký số, nên Windows SmartScreen sẽ cảnh báo khi mở lần đầu trên máy khác. Trước khi phát rộng trong công ty, nên ký bằng chứng thư của công ty hoặc phát qua kho nội bộ kèm mã SHA-256.

## Bản quyền

© 2026 Khoa Nguyen · [khoabug.com](https://khoabug.com/) · [LinkedIn](https://www.linkedin.com/in/khoanguyenit/)

Dòng này cũng nằm ở cuối cửa sổ app. Bấm vào một trong hai liên kết thì trang tương ứng mở trong trình duyệt mặc định; hai địa chỉ được ghi cố định trong phần Go, trang giao diện không mở được địa chỉ nào khác.
