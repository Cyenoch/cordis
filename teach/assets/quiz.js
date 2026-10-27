// 通用练习组件：即时反馈的选择/分类题。
//
// 用法（无需其它 JS）：
//   <div class="pick" data-answer="注册期">
//     <span class="q">题干…</span>
//     <div class="choices">
//       <button data-choice="生成期">生成期</button>
//       <button data-choice="注册期">注册期</button>
//     </div>
//     <div class="why">解释…</div>
//   </div>
//   页面上放一个 <span class="tally" data-tally></span> 显示进度。
//
// 行为：点对 → 锁定并展开解释；点错 → 标红该选项，仍可重试。
// 可打印：打印样式会展开所有解释。

(function () {
  "use strict";

  var picks = Array.prototype.slice.call(document.querySelectorAll(".pick"));
  var tallies = Array.prototype.slice.call(document.querySelectorAll("[data-tally]"));
  var solved = new Set();

  function report() {
    if (!tallies.length || !picks.length) return;
    var text = "已解出 " + solved.size + " / " + picks.length;
    tallies.forEach(function (node) { node.textContent = text; });
  }

  function solve(pick) {
    if (pick.classList.contains("solved")) return;
    pick.classList.add("solved");
    solved.add(pick);
    Array.prototype.forEach.call(pick.querySelectorAll("button"), function (button) {
      button.disabled = true;
      if (button.dataset.choice === pick.dataset.answer) button.classList.add("ok");
    });
    report();
  }

  picks.forEach(function (pick) {
    var answer = pick.dataset.answer || "";
    pick.setAttribute("role", "group");
    Array.prototype.forEach.call(pick.querySelectorAll("button[data-choice]"), function (button) {
      if (button.dataset.choice === answer) button.setAttribute("aria-pressed", "false");
      button.addEventListener("click", function () {
        if (pick.classList.contains("solved")) return;
        if (button.dataset.choice === answer) {
          solve(pick);
          return;
        }
        button.classList.add("bad");
        button.setAttribute("aria-pressed", "true");
      });
    });
  });

  // 键盘：Tab + Enter/Space 由原生 button 提供；此处仅补 Escape 重置单题。
  document.addEventListener("keydown", function (event) {
    if (event.key !== "Escape") return;
    var active = document.activeElement && document.activeElement.closest(".pick");
    if (!active) return;
    active.classList.remove("solved");
    solved.delete(active);
    Array.prototype.forEach.call(active.querySelectorAll("button"), function (button) {
      button.disabled = false;
      button.classList.remove("ok", "bad");
      button.setAttribute("aria-pressed", "false");
    });
    report();
  });

  report();

  // 打印/导出 PDF 时展开所有「回忆」块，保证纸张上能看到答案。
  window.addEventListener("beforeprint", function () {
    Array.prototype.forEach.call(document.querySelectorAll("details"), function (details) {
      details.open = true;
    });
  });
})();
