"use strict";

const $ = (id) => document.getElementById(id);
const paginas = {
  geral: ["Visão geral", "Estado do cluster em tempo real"],
  nos: ["Nós", "Membros, armazenamento e latência de cada nó"],
  anel: ["Anel", "Como o espaço de chaves está dividido entre os nós"],
  chaves: ["Chaves", "Navegue, edite e apague chaves por prefixo"],
  terminal: ["Terminal", "Os mesmos comandos da CLI, direto no navegador"],
};
const tons = ["#000000", "#4D4B4B", "#8F8C8B", "#BAB7B6", "#333232", "#6E6C6B", "#D3D1D0", "#1E1D1D"];
const tonsEscuro = ["#FFFFFF", "#BAB7B6", "#8F8C8B", "#D3D1D0", "#6E6C6B", "#E8E7E6", "#4D4B4B", "#A3A09F"];
let estado = { pagina: "geral", resumo: null, topologia: null, depois: "", historico: [], posHist: 0, timer: null };

function escapar(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

async function api(caminho, opcoes = {}) {
  const cab = { "X-Leme": "1" };
  if (opcoes.corpo !== undefined) cab["Content-Type"] = "application/json";
  const r = await fetch("/api/v1/" + caminho, { method: opcoes.metodo || "GET", headers: cab, body: opcoes.corpo !== undefined ? JSON.stringify(opcoes.corpo) : undefined, credentials: "same-origin" });
  const dados = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(dados.erro || "falha " + r.status);
  return dados;
}

function aviso(msg) {
  const a = $("aviso");
  a.textContent = msg;
  a.classList.add("visivel");
  clearTimeout(aviso.t);
  aviso.t = setTimeout(() => a.classList.remove("visivel"), 2600);
}

const fmt = {
  num: (n) => new Intl.NumberFormat("pt-BR").format(n || 0),
  bytes: (n) => {
    if (!n) return "0 B";
    const u = ["B", "KiB", "MiB", "GiB", "TiB"];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i ? n.toFixed(1).replace(".", ",") : n) + " " + u[i];
  },
  ms: (v) => (v || 0).toFixed(2).replace(".", ","),
  dur: (s) => {
    s = Math.floor(s);
    if (s < 60) return s + "s";
    if (s < 3600) return Math.floor(s / 60) + "m" + String(s % 60).padStart(2, "0") + "s";
    if (s < 172800) return Math.floor(s / 3600) + "h" + String(Math.floor((s % 3600) / 60)).padStart(2, "0") + "m";
    return Math.floor(s / 86400) + "d" + String(Math.floor((s % 86400) / 3600)).padStart(2, "0") + "h";
  },
  data: (ms) => new Date(ms).toLocaleString("pt-BR", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" }),
  ttl: (exp) => {
    if (!exp) return "—";
    const s = (exp - Date.now()) / 1000;
    return s <= 0 ? "expirada" : fmt.dur(s);
  },
  pct: (v) => ((v || 0) * 100).toFixed(1).replace(".", ",") + "%",
};

function opsDe(st) {
  return (st.operacoes || []).filter((o) => !/^(REPLICA|GOSSIP|SCAN_LOCAL|ENTRAR)/.test(o.op)).reduce((a, o) => a + o.total, 0);
}

function p99De(st, op) {
  const o = (st.operacoes || []).find((x) => x.op === op);
  return o ? o.p99_ms : 0;
}

const tonsTampa = ["#FFFFFF", "#8F8C8B", "#4D4B4B", "#D3D1D0", "#6E6C6B", "#BAB7B6", "#333232", "#A3A09F"];

function cores(tampa) {
  if (tampa) return tonsTampa;
  return document.documentElement.classList.contains("dark") ? tonsEscuro : tons;
}

function navegar() {
  const p = (location.hash || "#geral").slice(1);
  estado.pagina = paginas[p] ? p : "geral";
  document.querySelectorAll(".pagina").forEach((s) => (s.hidden = s.dataset.secao !== estado.pagina));
  document.querySelectorAll(".navegacao a").forEach((a) => a.classList.toggle("ativo", a.dataset.pagina === estado.pagina));
  $("titulo").textContent = paginas[estado.pagina][0];
  $("subtitulo").textContent = paginas[estado.pagina][1];
  $("menu").classList.remove("aberto");
  if (estado.pagina === "chaves" && !$("c-tabela").children.length) carregarChaves(true);
  if (estado.pagina === "terminal") setTimeout(() => $("t-entrada").focus(), 30);
  atualizar();
}

async function atualizar() {
  if (estado.pagina === "chaves" || estado.pagina === "terminal") return;
  try {
    const [r, t] = await Promise.all([api("resumo"), api("topologia")]);
    estado.resumo = r.resumo;
    estado.topologia = t;
    $("versao").textContent = "console " + r.versao;
    $("cluster-nome").textContent = "cluster " + r.resumo.cluster;
    $("conexao").className = "ponto " + (r.resumo.saude === "saudável" ? "ok" : r.resumo.saude === "degradado" ? "aviso" : "erro");
    $("atualizado").textContent = "atualizado às " + new Date(r.agora).toLocaleTimeString("pt-BR");
    if (estado.pagina === "geral") desenharGeral();
    if (estado.pagina === "nos") desenharNos();
    if (estado.pagina === "anel") desenharAnel();
  } catch (e) {
    $("conexao").className = "ponto erro";
    $("cluster-nome").textContent = "sem conexão";
    $("atualizado").textContent = e.message;
  }
}

function desenharGeral() {
  const r = estado.resumo;
  const saude = r.saude;
  $("g-saude").textContent = saude.charAt(0).toUpperCase() + saude.slice(1);
  $("g-saude-ponto").className = "ponto grande " + (saude === "saudável" ? "ok" : saude === "degradado" ? "aviso" : "erro");
  $("g-chaves").textContent = fmt.num(r.chaves_logicas);
  $("g-copias").textContent = fmt.num(r.chaves_fisicas);
  $("g-cluster").textContent = "cluster " + r.cluster;
  $("g-replicacao").textContent = r.replicas + " cópias · quórum " + (Math.floor(r.replicas / 2) + 1);
  $("g-nos").textContent = r.vivos + "/" + r.membros;
  $("g-nos-det").textContent = r.suspeitos + " suspeitos · " + r.mortos + " fora do ar";
  $("g-ops").textContent = fmt.num(r.operacoes);
  $("g-ops-det").textContent = fmt.num(r.erros) + " erros";
  $("g-p99get").textContent = fmt.ms(r.p99_get_ms);
  $("g-p99put").textContent = fmt.ms(r.p99_put_ms);
  $("g-disco").textContent = fmt.bytes(r.bytes_disco);
  $("g-disco-det").textContent = fmt.bytes(r.bytes_vivos) + " em registros vivos";
  $("g-dicas").textContent = fmt.num(r.dicas_pendentes);
  $("g-dicas-det").textContent = "dicas pendentes · " + fmt.num(r.reparos_de_leitura) + " reparos";
  const estados = Object.fromEntries((estado.topologia.membros || []).map((m) => [m.id, m.estado]));
  $("g-tabela").innerHTML = (r.nos || []).map((n) => `<tr><td><span class="ponto ${estados[n.id] || "vivo"}"></span></td><td>${escapar(n.id)}</td><td class="mono">${escapar(n.endereco)}</td><td class="d">${fmt.num(n.armazenamento.chaves)}</td><td class="d">${fmt.bytes(n.armazenamento.bytes_disco)}</td><td class="d">${fmt.num(opsDe(n))}</td><td class="d">${fmt.dur(n.tempo_no_ar_s)}</td></tr>`).join("") || `<tr><td colspan="7" class="vazio">Nenhum nó respondeu</td></tr>`;
  desenharAnelSVG($("g-anel"), 220, 84, false);
}

function desenharNos() {
  const t = estado.topologia;
  const st = Object.fromEntries((estado.resumo.nos || []).map((n) => [n.id, n]));
  $("n-tabela").innerHTML = (t.membros || []).map((m) => {
    const n = st[m.id];
    const a = n ? n.armazenamento : null;
    return `<tr><td><span class="ponto ${m.estado}"></span></td><td>${escapar(m.id)}</td><td class="mono">${escapar(m.endereco)}</td><td>${escapar(m.estado)}</td><td class="d">${a ? fmt.num(a.chaves) : "—"}</td><td class="d">${a ? fmt.num(a.lapides) : "—"}</td><td class="d">${a ? fmt.bytes(a.bytes_disco) : "—"}</td><td class="d">${a ? fmt.pct(a.fragmentacao) : "—"}</td><td class="d">${n ? fmt.num(opsDe(n)) : "—"}</td><td class="d">${n ? fmt.ms(p99De(n, "GET")) + " ms" : "—"}</td><td class="d">${fmt.pct((t.participacao || {})[m.id])}</td><td class="d">${n ? fmt.dur(n.tempo_no_ar_s) : "—"}</td></tr>`;
  }).join("");
}

function desenharAnelSVG(svg, tam, raio, grande, marca) {
  const t = estado.topologia;
  if (!t) return;
  const ids = Object.keys(t.participacao || {}).sort();
  const paleta = cores(grande);
  const cor = Object.fromEntries(ids.map((id, i) => [id, paleta[i % paleta.length]]));
  const c = tam / 2;
  const traco = grande ? 22 : 16;
  let s = `<circle cx="${c}" cy="${c}" r="${raio}" fill="none" stroke="${grande ? "rgba(255,255,255,.12)" : "var(--parede)"}" stroke-width="${traco}"/>`;
  const pts = (t.pontos || []).slice().sort((a, b) => a.posicao - b.posicao);
  for (let i = 0; i < pts.length; i++) {
    const p = pts[i];
    const prox = i + 1 < pts.length ? pts[i + 1].posicao : pts[0].posicao + 1;
    const a0 = p.posicao * 2 * Math.PI - Math.PI / 2;
    const a1 = prox * 2 * Math.PI - Math.PI / 2;
    const x0 = c + raio * Math.cos(a0), y0 = c + raio * Math.sin(a0);
    const x1 = c + raio * Math.cos(a1), y1 = c + raio * Math.sin(a1);
    const grandeArco = a1 - a0 > Math.PI ? 1 : 0;
    const corArco = cor[p.no];
    s += `<path d="M${x0.toFixed(2)} ${y0.toFixed(2)} A${raio} ${raio} 0 ${grandeArco} 1 ${x1.toFixed(2)} ${y1.toFixed(2)}" fill="none" stroke="${corArco}" stroke-width="${traco}"/>`;
  }
  if (marca !== undefined) {
    const a = marca * 2 * Math.PI - Math.PI / 2;
    const x = c + (raio + traco) * Math.cos(a), y = c + (raio + traco) * Math.sin(a);
    const xi = c + (raio - traco) * Math.cos(a), yi = c + (raio - traco) * Math.sin(a);
    s += `<line x1="${xi}" y1="${yi}" x2="${x}" y2="${y}" stroke="#E89A87" stroke-width="3" stroke-linecap="round"/><circle cx="${x}" cy="${y}" r="5" fill="#E89A87"/>`;
  }
  const corTexto = grande ? "#FFFFFF" : "var(--texto)";
  const r = estado.resumo;
  s += `<text x="${c}" y="${c - (grande ? 6 : 2)}" text-anchor="middle" font-family="Bricolage Grotesque" font-weight="300" font-size="${grande ? 44 : 30}" fill="${corTexto}" style="font-variation-settings:'opsz' 96,'wdth' 80">${ids.length}</text>`;
  s += `<text x="${c}" y="${c + (grande ? 22 : 16)}" text-anchor="middle" font-family="Instrument Sans" font-size="${grande ? 13 : 11}" fill="${grande ? "#BAB7B6" : "var(--texto-3)"}">${ids.length === 1 ? "nó" : "nós"} · ${t.vnodes} vnodes</text>`;
  svg.innerHTML = s;
  void r;
}

function desenharAnel(marca) {
  const t = estado.topologia;
  desenharAnelSVG($("a-anel"), 320, 120, true, marca);
  const ids = Object.keys(t.participacao || {}).sort();
  const max = Math.max(...ids.map((id) => t.participacao[id]), 0.0001);
  $("a-info").textContent = t.replicas + " cópias por chave";
  $("a-legenda").innerHTML = ids.map((id, i) => `<div class="legenda-item"><span class="legenda-cor" style="background:${cores(true)[i % 8]}"></span><span>${escapar(id)}</span><span class="legenda-barra"><span style="width:${((t.participacao[id] / max) * 100).toFixed(1)}%"></span></span><span class="d">${fmt.pct(t.participacao[id])}</span></div>`).join("");
}

async function localizar(ev) {
  ev.preventDefault();
  const k = $("a-chave").value.trim();
  if (!k) return;
  try {
    const r = await api("localizar?k=" + encodeURIComponent(k));
    desenharAnel(r.posicao);
    $("a-resultado").innerHTML = `<div class="replicas">${r.replicas.map((m, i) => `<div><span class="papel">${i === 0 ? "primária" : "réplica"}</span><span class="ponto ${m.estado}"></span><span>${escapar(m.id)}</span><span class="detalhe mono">${escapar(m.endereco)}</span></div>`).join("")}</div><p class="detalhe">posição ${fmt.pct(r.posicao)} no anel</p>`;
  } catch (e) {
    aviso(e.message);
  }
}

async function carregarChaves(reiniciar) {
  if (reiniciar) {
    estado.depois = "";
    $("c-tabela").innerHTML = "";
  }
  const prefixo = $("c-prefixo").value.trim();
  try {
    const r = await api(`chaves?prefixo=${encodeURIComponent(prefixo)}&depois=${encodeURIComponent(estado.depois)}&limite=50`);
    const linhas = r.itens.map((it) => `<tr data-chave="${escapar(it.chave)}"><td class="mono">${escapar(it.chave)}</td><td class="valor">${it.binario ? "<em>binário</em>" : escapar(it.valor)}</td><td class="d">${fmt.bytes(it.tamanho)}</td><td class="d">${fmt.data(it.gravado)}</td><td class="d">${fmt.ttl(it.expira)}</td></tr>`).join("");
    $("c-tabela").insertAdjacentHTML("beforeend", linhas);
    if (!$("c-tabela").children.length) $("c-tabela").innerHTML = `<tr><td colspan="5" class="vazio">Nenhuma chave${prefixo ? " com o prefixo " + escapar(prefixo) : ""}</td></tr>`;
    if (r.itens.length) estado.depois = r.itens[r.itens.length - 1].chave;
    $("c-mais").hidden = !r.mais;
    const n = $("c-tabela").querySelectorAll("tr[data-chave]").length;
    $("c-total").textContent = n + (n === 1 ? " chave" : " chaves") + (r.mais ? " · há mais" : "");
  } catch (e) {
    aviso(e.message);
  }
}

function abrirGaveta(titulo, html) {
  $("gv-titulo").textContent = titulo;
  $("gv-corpo").innerHTML = html;
  $("gaveta").classList.add("aberta");
  $("gaveta").setAttribute("aria-hidden", "false");
  $("veu").hidden = false;
}

function fecharGaveta() {
  $("gaveta").classList.remove("aberta");
  $("gaveta").setAttribute("aria-hidden", "true");
  $("veu").hidden = true;
}

async function verChave(k) {
  try {
    const r = await api("chave?k=" + encodeURIComponent(k));
    const it = r.item;
    let valor = it.binario ? `(${fmt.bytes(it.tamanho)} binários)` : it.valor;
    try { if (!it.binario && /^[\[{]/.test(valor.trim())) valor = JSON.stringify(JSON.parse(valor), null, 2); } catch (e) {}
    abrirGaveta(k, `
      <pre class="valor-bloco">${escapar(valor)}</pre>
      <dl class="pares">
        <dt>tamanho</dt><dd>${fmt.bytes(it.tamanho)}</dd>
        <dt>versão</dt><dd class="mono">${escapar(it.versao)}</dd>
        <dt>gravado em</dt><dd>${fmt.data(it.gravado)}</dd>
        <dt>ttl</dt><dd>${fmt.ttl(it.expira)}</dd>
        <dt>réplicas</dt><dd>${r.replicas.map((m) => `<span class="ponto ${m.estado}"></span> ${escapar(m.id)}`).join(" &nbsp; ")}</dd>
        <dt>respostas</dt><dd>${r.respostas} · via ${escapar(r.coordenador)}</dd>
      </dl>
      <div class="acoes-gaveta"><button class="botao escuro" id="gv-editar" type="button">Editar</button><button class="botao perigo" id="gv-apagar" type="button">Apagar</button></div>`);
    $("gv-editar").onclick = () => formularioChave(k, it.binario ? "" : it.valor, it.versao);
    $("gv-apagar").onclick = async () => {
      if (!confirm("Apagar " + k + "?")) return;
      try {
        await api("chave?k=" + encodeURIComponent(k), { metodo: "DELETE" });
        fecharGaveta();
        aviso("Chave apagada");
        carregarChaves(true);
      } catch (e) { aviso(e.message); }
    };
  } catch (e) {
    aviso(e.message);
  }
}

function formularioChave(k, valor, versao) {
  abrirGaveta(k ? "Editar " + k : "Nova chave", `
    <form id="gv-form" class="gaveta-corpo" style="padding:0">
      <label class="campo-rotulo">Chave<input class="campo" id="gv-chave" value="${escapar(k || "")}" ${k ? "readonly" : ""} required></label>
      <label class="campo-rotulo">Valor<textarea class="campo" id="gv-valor">${escapar(valor || "")}</textarea></label>
      <label class="campo-rotulo">Expira em (opcional: 30s, 15m, 2h)<input class="campo" id="gv-ttl" placeholder="sem expiração"></label>
      ${versao ? `<label class="campo-rotulo"><span><input type="checkbox" id="gv-cas" checked> só gravar se ninguém alterou desde a versão ${escapar(versao)}</span></label>` : ""}
      <div class="acoes-gaveta"><button class="botao escuro" type="submit">Gravar</button></div>
    </form>`);
  $("gv-form").onsubmit = async (ev) => {
    ev.preventDefault();
    const corpo = { chave: $("gv-chave").value.trim(), valor: $("gv-valor").value, ttl: $("gv-ttl").value.trim() };
    if (versao && $("gv-cas") && $("gv-cas").checked) corpo.se_versao = versao;
    try {
      const r = await api("chave", { metodo: "PUT", corpo });
      aviso(`Gravado · ${r.confirmacoes}/${r.replicas} réplicas`);
      fecharGaveta();
      carregarChaves(true);
    } catch (e) {
      aviso(e.message);
    }
  };
  setTimeout(() => (k ? $("gv-valor") : $("gv-chave")).focus(), 50);
}

function terminalEscrever(cmd, saida, erro, tempo) {
  const b = document.createElement("div");
  b.className = "t-bloco";
  if (cmd !== null) {
    const c = document.createElement("div");
    c.className = "t-comando";
    c.textContent = cmd;
    b.appendChild(c);
  }
  if (saida) {
    const s = document.createElement("div");
    s.className = "t-resultado";
    s.textContent = saida.replace(/\n$/, "");
    b.appendChild(s);
  }
  if (erro) {
    const e = document.createElement("div");
    e.className = "t-erro";
    e.textContent = "erro: " + erro;
    b.appendChild(e);
  }
  if (tempo !== undefined) {
    const t = document.createElement("div");
    t.className = "t-tempo";
    t.textContent = "(" + fmt.ms(tempo) + " ms)";
    b.appendChild(t);
  }
  $("t-saida").appendChild(b);
  $("t-saida").scrollTop = $("t-saida").scrollHeight;
}

async function terminalEnviar(ev) {
  ev.preventDefault();
  const linha = $("t-entrada").value.trim();
  $("t-entrada").value = "";
  if (!linha) return;
  if (estado.historico[estado.historico.length - 1] !== linha) estado.historico.push(linha);
  estado.posHist = estado.historico.length;
  if (linha === "limpar" || linha === "clear") {
    $("t-saida").innerHTML = "";
    return;
  }
  try {
    const r = await api("executar", { metodo: "POST", corpo: { linha } });
    terminalEscrever(linha, r.saida, r.erro, r.duracao_ms);
  } catch (e) {
    terminalEscrever(linha, "", e.message);
  }
}

function terminalTeclas(ev) {
  if (ev.key === "ArrowUp" && estado.posHist > 0) {
    estado.posHist--;
    $("t-entrada").value = estado.historico[estado.posHist];
    ev.preventDefault();
  } else if (ev.key === "ArrowDown") {
    estado.posHist = Math.min(estado.posHist + 1, estado.historico.length);
    $("t-entrada").value = estado.historico[estado.posHist] || "";
    ev.preventDefault();
  } else if (ev.key === "l" && ev.ctrlKey) {
    $("t-saida").innerHTML = "";
    ev.preventDefault();
  }
}

function alternarTema() {
  const escuro = document.documentElement.classList.toggle("dark");
  try { localStorage.setItem("leme-tema", escuro ? "escuro" : "claro"); } catch (e) {}
  if (estado.topologia) {
    if (estado.pagina === "geral") desenharGeral();
    if (estado.pagina === "anel") desenharAnel();
  }
}

function iniciar() {
  window.addEventListener("hashchange", navegar);
  $("tema").onclick = alternarTema;
  $("abrir-menu").onclick = () => $("menu").classList.toggle("aberto");
  $("veu").onclick = fecharGaveta;
  $("gv-fechar").onclick = fecharGaveta;
  document.addEventListener("keydown", (ev) => { if (ev.key === "Escape") fecharGaveta(); });
  $("a-form").onsubmit = localizar;
  $("c-form").onsubmit = (ev) => { ev.preventDefault(); carregarChaves(true); };
  $("c-mais").onclick = () => carregarChaves(false);
  $("c-nova").onclick = () => formularioChave("", "", "");
  $("c-tabela").onclick = (ev) => { const tr = ev.target.closest("tr[data-chave]"); if (tr) verChave(tr.dataset.chave); };
  $("t-form").onsubmit = terminalEnviar;
  $("t-entrada").onkeydown = terminalTeclas;
  $("t-terminal").onclick = (ev) => { if (!window.getSelection().toString()) $("t-entrada").focus(); };
  terminalEscrever(null, "Leme · terminal web. Digite ajuda para ver os comandos.\nExemplos: put usuario:1 Ana · get usuario:1 --detalhes · scan usuario: · cluster status");
  navegar();
  estado.timer = setInterval(atualizar, 3000);
}

iniciar();
