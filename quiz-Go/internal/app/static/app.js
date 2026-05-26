async function postJSON(url, data) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
  return { ok: res.ok, status: res.status, json: await res.json().catch(() => null) };
}

function qs(sel) { return document.querySelector(sel); }
function qsa(sel) { return Array.from(document.querySelectorAll(sel)); }

// Quiz submission
(function initQuiz() {
  const quiz = qs("#quiz");
  const btn = qs("#submitBtn");
  const result = qs("#result");
  if (!quiz || !btn) return;

  btn.addEventListener("click", async () => {
    const testId = quiz.getAttribute("data-test-id");
    const answers = {};
    qsa(".qcard").forEach((card) => {
      const qid = card.getAttribute("data-qid");
      const checked = card.querySelector("input[type=radio]:checked");
      if (checked) answers[qid] = Number(checked.value);
    });
    if (Object.keys(answers).length === 0) {
      alert("Выберите хотя бы один ответ.");
      return;
    }
    btn.disabled = true;
    const { ok, json } = await postJSON(`/tests/${testId}/submit`, { answers });
    btn.disabled = false;
    if (!ok || !json) {
      alert("Не удалось сохранить результат.");
      return;
    }
    result.classList.remove("hidden");
    result.innerHTML = `<b>Результат:</b> ${json.correct}/${json.total} (${json.percent}%). <a href="/history">Открыть историю</a>`;
    result.scrollIntoView({ behavior: "smooth", block: "nearest" });
  });
})();

// Admin create test
(function initAdminNewTest() {
  const ta = qs("#adminNewTestJson");
  const btn = qs("#adminCreateTestBtn");
  const msg = qs("#adminCreateTestMsg");
  if (!ta || !btn) return;

  btn.addEventListener("click", async () => {
    msg.textContent = "";
    let payload;
    try {
      payload = JSON.parse(ta.value);
    } catch (e) {
      msg.textContent = "Невалидный JSON.";
      return;
    }
    btn.disabled = true;
    const { ok, status, json } = await postJSON("/admin/tests/new", payload);
    btn.disabled = false;
    if (!ok) {
      msg.textContent = `Ошибка создания (HTTP ${status}). Проверьте поля и что options ровно 4.`;
      return;
    }
    msg.textContent = `Создано. ID: ${json.id}. Перейдите в каталог тестов.`;
  });
})();

