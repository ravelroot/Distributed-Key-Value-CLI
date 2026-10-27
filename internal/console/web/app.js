"use strict";

const $ = (id) => document.getElementById(id);
const paginas = {
  geral: ["Visão geral", "Estado do cluster em tempo real", "o"],
  nos: ["Nós", "Membros, armazenamento e latência de cada nó", "n"],
  anel: ["Anel", "Como o espaço de chaves está dividido entre os nós", "a"],
  chaves: ["Chaves", "Navegue, edite, exporte e importe chaves por prefixo", "c"],
  terminal: ["Terminal", "Os mesmos comandos da CLI, direto no navegador", "t"],
  diagnostico: ["Diagnóstico", "Verificações de saúde com o que fazer em cada caso", "d"],
  integracoes: ["Integrações", "API REST, exemplos de código, métricas e implantação", "i"],
};
const MAX_HIST = 60;
const estado = {
  pagina: "geral", resumo: null, topologia: null, consistencia: "quorum", depois: "",
  historico: [], posHist: 0, timer: null, membros: null, eventos: [],
  serie: { ops: [], get: [], put: [] }, anterior: null, integracao: null, aba: "curl", mostrarToken: false,
  marcaAnel: undefined, importacao: null,
};

function escapar(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

async function api(caminho, opcoes = {}) {
  const cab = { "X-Leme": "1" };
  let corpo;
  if (opcoes.bruto !== undefined) corpo = opcoes.bruto;
  else if (opcoes.corpo !== undefined) { cab["Content-Type"] = "application/json"; corpo = JSON.stringify(opcoes.corpo); }
  const r = await fetch("/api/v1/" + caminho, { method: opcoes.metodo || "GET", headers: cab, body: corpo, credentials: "same-origin" });
  if (opcoes.blob && r.ok) return r;
  const dados = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(dados.erro || "o console respondeu " + r.status);
  return dados;
}

function aviso(msg, erro) {
  const a = $("aviso");
  a.textContent = msg;
  a.classList.toggle("erro", !!erro);
  a.classList.add("visivel");
  clearTimeout(aviso.t);
  aviso.t = setTimeout(() => a.classList.remove("visivel"), erro ? 4200 : 2600);
}

const fmt = {
  num: (n) => new Intl.NumberFormat("pt-BR").format(n || 0),
  dec: (n, c = 1) => new Intl.NumberFormat("pt-BR", { minimumFractionDigits: c, maximumFractionDigits: c }).format(n || 0),
  bytes: (n) => {
    if (!n) return "0 B";
    const u = ["B", "KiB", "MiB", "GiB", "TiB"];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i ? fmt.dec(n) : n) + " " + u[i];
  },
  ms: (v) => fmt.dec(v || 0, 2),
  dur: (s) => {
    s = Math.floor(s);
    if (s < 60) return s + "s";
    if (s < 3600) return Math.floor(s / 60) + "m" + String(s % 60).padStart(2, "0") + "s";
    if (s < 172800) return Math.floor(s / 3600) + "h" + String(Math.floor((s % 3600) / 60)).padStart(2, "0") + "m";
    return Math.floor(s / 86400) + "d" + String(Math.floor((s % 86400) / 3600)).padStart(2, "0") + "h";
  },
  data: (ms) => new Date(ms).toLocaleString("pt-BR", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" }),
  hora: (ms) => new Date(ms).toLocaleTimeString("pt-BR"),
  ttl: (exp) => {
    if (!exp) return "—";
    const s = (exp - Date.now()) / 1000;
    return s <= 0 ? "expirada" : fmt.dur(s);
  },
  pct: (v) => fmt.dec((v || 0) * 100) + "%",
};

function opsDe(st) {
  return (st.operacoes || []).filter((o) => !/^(REPLICA|GOSSIP|SCAN_LOCAL|ENTRAR)/.test(o.op)).reduce((a, o) => a + o.total, 0);
}
function p99De(st, op) {
  const o = (st.operacoes || []).find((x) => x.op === op);
  return o ? o.p99_ms : 0;
}
function corNo(i) { return `var(--no-${i % 8})`; }
function idsNos() { return Object.keys((estado.topologia && estado.topologia.participacao) || {}).sort(); }
function quorum(r) { const rep = Math.min(r.replicas, r.membros || r.replicas); return Math.floor(rep / 2) + 1; }
function classeSaude(s) { return s === "saudável" ? "ok" : s === "degradado" ? "aviso" : "erro"; }

/* Eventos: mudanças percebidas pelo próprio console */
function registrarEvento(texto, tipo) {
  estado.eventos.unshift({ quando: Date.now(), texto, tipo: tipo || "ok" });
  estado.eventos = estado.eventos.slice(0, 80);
  if (estado.pagina === "geral") desenharEventos();
}

function desenharEventos() {
  const el = $("g-eventos");
  if (!estado.eventos.length) {
    el.innerHTML = `<li class="vazio">Nada aconteceu ainda. Mudanças de estado dos nós e gravações feitas por aqui aparecem nesta lista.</li>`;
    return;
  }
  el.innerHTML = estado.eventos.map((e) => `<li><time>${fmt.hora(e.quando)}</time><span class="ponto ${e.tipo}"></span><span>${escapar(e.texto)}</span></li>`).join("");
}

function compararMembros(t) {
  const atual = Object.fromEntries((t.membros || []).map((m) => [m.id, m.estado]));
  if (estado.membros) {
    for (const [id, st] of Object.entries(atual)) {
      const antes = estado.membros[id];
      if (antes === undefined) registrarEvento(`${id} entrou no cluster`, "ok");
      else if (antes !== st) {
        const txt = { vivo: "voltou ao ar", suspeito: "parou de responder (suspeito)", morto: "está fora do ar", saiu: "saiu do anel" }[st] || "mudou para " + st;
        registrarEvento(`${id} ${txt}`, st === "vivo" ? "ok" : st === "suspeito" ? "aviso" : "erro");
      }
    }
    for (const id of Object.keys(estado.membros)) if (!(id in atual)) registrarEvento(`${id} deixou a lista de membros`, "aviso");
  }
  estado.membros = atual;
}

function acumularSerie(r, agora) {
  if (estado.anterior) {
    const dt = (agora - estado.anterior.quando) / 1000;
    const dops = r.operacoes - estado.anterior.ops;
    if (dt > 0 && dops >= 0) estado.serie.ops.push(dops / dt);
  }
  estado.serie.get.push(r.p99_get_ms || 0);
  estado.serie.put.push(r.p99_put_ms || 0);
  for (const k of Object.keys(estado.serie)) if (estado.serie[k].length > MAX_HIST) estado.serie[k].shift();
  estado.anterior = { quando: agora, ops: r.operacoes };
}

function faisca(svg, valores) {
  if (!valores.length) { svg.innerHTML = ""; return; }
  const max = Math.max(...valores, 0.0001);
  const n = valores.length;
  const pts = valores.map((v, i) => [n === 1 ? 120 : (i / (n - 1)) * 120, 26 - (v / max) * 23]);
  if (pts.length === 1) pts.unshift([0, pts[0][1]]);
  const linha = pts.map((p) => p[0].toFixed(1) + "," + p[1].toFixed(1)).join(" ");
  const u = pts[pts.length - 1];
  svg.innerHTML = `<polygon class="area" points="${pts[0][0].toFixed(1)},28 ${linha} ${u[0].toFixed(1)},28"/><polyline class="traco" points="${linha}"/><circle class="ultimo" cx="${u[0].toFixed(1)}" cy="${u[1].toFixed(1)}" r="2"/>`;
}

/* Navegação */
function navegar() {
  const p = (location.hash || "#geral").slice(1);
  estado.pagina = paginas[p] ? p : "geral";
  document.querySelectorAll(".pagina").forEach((s) => (s.hidden = s.dataset.secao !== estado.pagina));
  document.querySelectorAll(".navegacao a").forEach((a) => {
    const ativo = a.dataset.pagina === estado.pagina;
    a.classList.toggle("ativo", ativo);
    if (ativo) a.setAttribute("aria-current", "page"); else a.removeAttribute("aria-current");
  });
  $("titulo").textContent = paginas[estado.pagina][0];
  $("subtitulo").textContent = paginas[estado.pagina][1];
  document.title = paginas[estado.pagina][0] + " · Leme";
  fecharMenu();
  if (estado.pagina === "chaves" && !$("c-tabela").children.length) carregarChaves(true);
  if (estado.pagina === "terminal") setTimeout(() => $("t-entrada").focus(), 30);
  if (estado.pagina === "diagnostico" && !$("d-lista").children.length) diagnosticar();
  if (estado.pagina === "integracoes") carregarIntegracao();
  if (estado.pagina === "geral") desenharEventos();
  desenharPagina();
}

function desenharPagina() {
  if (!estado.resumo || !estado.topologia) return;
  if (estado.pagina === "geral") desenharGeral();
  if (estado.pagina === "nos") desenharNos();
  if (estado.pagina === "anel") desenharAnel(estado.marcaAnel);
}

async function atualizar() {
  const vivo = $("f-vivo");
  try {
    const [r, t] = await Promise.all([api("resumo"), api("topologia")]);
    const primeira = !estado.resumo;
    estado.resumo = r.resumo;
    estado.topologia = t;
    estado.consistencia = r.consistencia || "quorum";
    acumularSerie(r.resumo, r.agora);
    compararMembros(t);
    if (primeira) registrarEvento(`Console conectado ao cluster ${r.resumo.cluster}`, "ok");
    $("versao").textContent = "console " + r.versao;
    $("f-cluster").textContent = r.resumo.cluster;
    $("f-ponto").className = "ponto " + classeSaude(r.resumo.saude);
    $("f-nos").textContent = r.resumo.vivos + "/" + r.resumo.membros;
    $("f-rep").textContent = r.resumo.replicas + ", quórum " + quorum(r.resumo);
    $("f-cons").textContent = estado.consistencia;
    $("f-atualizado").textContent = "ao vivo, " + fmt.hora(r.agora);
    vivo.classList.remove("parado");
    vivo.classList.remove("pulsando"); void vivo.offsetWidth; vivo.classList.add("pulsando");
    desenharPagina();
  } catch (e) {
    $("f-ponto").className = "ponto erro";
    $("f-cluster").textContent = "sem conexão";
    $("f-atualizado").textContent = "parado: " + e.message;
    vivo.classList.add("parado");
    if (!atualizar.falhou) registrarEvento("Sem resposta do cluster: " + e.message, "erro");
    atualizar.falhou = true;
    return;
  }
  if (atualizar.falhou) registrarEvento("Conexão com o cluster restabelecida", "ok");
  atualizar.falhou = false;
}

function fatia(id, i) {
  const p = ((estado.topologia.participacao || {})[id]) || 0;
  const max = Math.max(...Object.values(estado.topologia.participacao || { x: 1 }), 0.0001);
  return `<div class="fatia"><span class="fatia-barra"><span style="width:${((p / max) * 100).toFixed(1)}%;background:${corNo(i)}"></span></span><span class="mono">${fmt.pct(p)}</span></div>`;
}

function desenharGeral() {
  const r = estado.resumo;
  const q = quorum(r);
  const partes = [`${r.vivos} de ${r.membros} ${r.membros === 1 ? "nó" : "nós"} no ar`, `quórum ${q} de ${Math.min(r.replicas, r.membros)}`];
  partes.push(r.dicas_pendentes ? `${fmt.num(r.dicas_pendentes)} ${r.dicas_pendentes === 1 ? "dica pendente" : "dicas pendentes"}` : "nenhuma dica pendente");
  if (r.saude === "sem quórum") partes.push("escritas com quórum vão falhar");
  $("g-ponto").className = "ponto " + classeSaude(r.saude);
  $("g-frase-texto").textContent = partes.join(", ").replace(/^./, (c) => c.toUpperCase()) + ".";
  $("g-chaves").textContent = fmt.num(r.chaves_logicas);
  $("g-chaves-det").textContent = fmt.num(r.chaves_fisicas) + " cópias gravadas";
  const ops = estado.serie.ops;
  $("g-ops").textContent = ops.length ? fmt.dec(ops[ops.length - 1]) : "—";
  $("g-ops-det").textContent = fmt.num(r.operacoes) + " no total, " + fmt.num(r.erros) + " erros";
  $("g-p99get").textContent = fmt.ms(r.p99_get_ms);
  $("g-p99put").textContent = fmt.ms(r.p99_put_ms);
  faisca($("g-faisca-ops"), estado.serie.ops);
  faisca($("g-faisca-get"), estado.serie.get);
  faisca($("g-faisca-put"), estado.serie.put);
  $("g-disco").textContent = fmt.bytes(r.bytes_disco);
  const vivos = r.bytes_disco ? r.bytes_vivos / r.bytes_disco : 0;
  $("g-disco-barra").style.width = (vivos * 100).toFixed(1) + "%";
  $("g-disco-det").textContent = fmt.pct(vivos) + " em registros vivos";
  const estados = Object.fromEntries((estado.topologia.membros || []).map((m) => [m.id, m.estado]));
  const ids = idsNos();
  $("g-tabela").innerHTML = (r.nos || []).map((n) => `<tr><td><span class="no"><span class="ponto ${estados[n.id] || "vivo"}" title="${escapar(estados[n.id] || "vivo")}"></span>${escapar(n.id)}</span></td><td class="mono fraco op">${escapar(n.endereco)}</td><td class="d">${fmt.num(n.armazenamento.chaves)}</td><td class="d">${fmt.bytes(n.armazenamento.bytes_disco)}</td><td class="d op2">${fmt.num(opsDe(n))}</td><td>${fatia(n.id, Math.max(ids.indexOf(n.id), 0))}</td><td class="d op">${fmt.dur(n.tempo_no_ar_s)}</td></tr>`).join("")
    || `<tr><td colspan="7" class="vazio"><strong>Nenhum nó respondeu</strong>Confira os endereços em --nos e se os processos lemed estão rodando.</td></tr>`;
  const lapides = (r.nos || []).reduce((a, n) => a + (n.armazenamento.lapides || 0), 0);
  const frag = (r.nos || []).reduce((a, n) => Math.max(a, n.armazenamento.fragmentacao || 0), 0);
  $("g-pend").innerHTML = `<dt>Dicas aguardando réplicas</dt><dd>${fmt.num(r.dicas_pendentes)}</dd><dt>Reparos de leitura</dt><dd>${fmt.num(r.reparos_de_leitura)}</dd><dt>Lápides</dt><dd>${fmt.num(lapides)}</dd><dt>Maior fragmentação</dt><dd>${fmt.pct(frag)}</dd><dt>Nós virtuais por nó</dt><dd>${fmt.num(r.vnodes)}</dd>`;
  mostrador($("g-anel"), 240, false);
}

function desenharNos() {
  const t = estado.topologia;
  const st = Object.fromEntries((estado.resumo.nos || []).map((n) => [n.id, n]));
  const ids = idsNos();
  $("n-tabela").innerHTML = (t.membros || []).map((m) => {
    const n = st[m.id];
    const a = n ? n.armazenamento : null;
    return `<tr><td><span class="no"><span class="ponto ${m.estado}" title="${escapar(m.estado)}"></span>${escapar(m.id)}</span></td><td class="mono fraco op">${escapar(m.endereco)}</td><td class="op">${escapar(m.estado)}</td><td class="d">${a ? fmt.num(a.chaves) : "—"}</td><td class="d op">${a ? fmt.num(a.lapides) : "—"}</td><td class="d">${a ? fmt.bytes(a.bytes_disco) : "—"}</td><td class="d op2">${a ? fmt.pct(a.fragmentacao) : "—"}</td><td class="d op">${n ? fmt.num(opsDe(n)) : "—"}</td><td class="d op">${n ? fmt.ms(p99De(n, "GET")) + " ms" : "—"}</td><td>${fatia(m.id, Math.max(ids.indexOf(m.id), 0))}</td><td class="d op">${n ? fmt.dur(n.tempo_no_ar_s) : "—"}</td></tr>`;
  }).join("") || `<tr><td colspan="11" class="vazio"><strong>Nenhum membro conhecido</strong>O cliente ainda não recebeu a topologia do cluster.</td></tr>`;
  $("n-fichas").innerHTML = (estado.resumo.nos || []).map((n) => {
    const est = (t.membros || []).find((m) => m.id === n.id);
    const etq = (lig, txt) => `<span class="etiqueta${lig ? " ligada" : ""}">${txt}${lig ? "" : " desligado"}</span>`;
    return `<div class="painel ficha"><div class="painel-cab"><h2><span class="ponto ${est ? est.estado : "vivo"}"></span>${escapar(n.id)}</h2><span>${etq(n.tls, "TLS")} ${etq(n.autenticacao, "token")}</span></div><dl class="pares">
      <dt>Versão</dt><dd>${escapar(n.versao)}</dd><dt>Go</dt><dd>${escapar(n.go)}</dd><dt>Sistema</dt><dd>${escapar(n.so)}</dd>
      <dt>Conexões (pares)</dt><dd>${fmt.num(n.conexoes)} (${fmt.num(n.conexoes_pares)})</dd><dt>Memória</dt><dd>${fmt.bytes(n.memoria_bytes)}</dd>
      <dt>Segmentos de log</dt><dd>${fmt.num(n.armazenamento.segmentos)}</dd><dt>Dicas entregues</dt><dd>${fmt.num(n.dicas_entregues)}</dd>
      <dt>Chaves transferidas</dt><dd>${fmt.num(n.chaves_transferidas)}</dd><dt>Falhas de autenticação</dt><dd>${fmt.num(n.falhas_de_autenticacao)}</dd>
    </dl></div>`;
  }).join("");
  const sem = (estado.resumo.falhas || []).length;
  $("n-nota").innerHTML = sem ? escapar(`${sem} ${sem === 1 ? "nó não respondeu" : "nós não responderam"} às estatísticas: ${estado.resumo.falhas.join("; ")}`) : "Fragmentação alta? Compacte pelo terminal com <code>node compact &lt;nó&gt;</code>, sem parar o nó.";
}

/* Mostrador de bússola: 360 marcas, rótulos a cada 30°, arcos por nó */
function mostrador(svg, tam, grande, marca) {
  const t = estado.topologia;
  if (!t) return;
  const ids = idsNos();
  const cor = Object.fromEntries(ids.map((id, i) => [id, corNo(i)]));
  const c = tam / 2;
  const rExt = c - (grande ? 6 : 4);
  const rotulos = grande;
  const rMarca = rotulos ? rExt - 16 : rExt;
  const traco = grande ? 16 : 11;
  const rArco = rMarca - (grande ? 24 : 16) - traco / 2;
  let s = "";
  for (let g = 0; g < 360; g += grande ? 1 : 2) {
    const tipo = g % 30 === 0 ? "grossa" : g % 10 === 0 ? "media" : "fina";
    const comp = tipo === "grossa" ? (grande ? 12 : 9) : tipo === "media" ? (grande ? 7 : 5) : (grande ? 3.5 : 2.5);
    const a = (g * Math.PI) / 180 - Math.PI / 2;
    const x0 = c + rMarca * Math.cos(a), y0 = c + rMarca * Math.sin(a);
    const x1 = c + (rMarca - comp) * Math.cos(a), y1 = c + (rMarca - comp) * Math.sin(a);
    s += `<line class="marca-${tipo}" x1="${x0.toFixed(2)}" y1="${y0.toFixed(2)}" x2="${x1.toFixed(2)}" y2="${y1.toFixed(2)}"/>`;
    if (rotulos && g % 30 === 0) {
      const xr = c + (rExt - 5) * Math.cos(a), yr = c + (rExt - 5) * Math.sin(a);
      s += `<text class="grau${g === 0 ? " grau-norte" : ""}" x="${xr.toFixed(1)}" y="${yr.toFixed(1)}" text-anchor="middle" dominant-baseline="central">${String(g).padStart(3, "0")}</text>`;
    }
  }
  s += `<circle class="trilho" cx="${c}" cy="${c}" r="${rArco}" stroke-width="${traco}"/>`;
  const pts = (t.pontos || []).slice().sort((a, b) => a.posicao - b.posicao);
  for (let i = 0; i < pts.length; i++) {
    const p = pts[i];
    const prox = i + 1 < pts.length ? pts[i + 1].posicao : pts[0].posicao + 1;
    const a0 = p.posicao * 2 * Math.PI - Math.PI / 2;
    const a1 = prox * 2 * Math.PI - Math.PI / 2;
    const x0 = c + rArco * Math.cos(a0), y0 = c + rArco * Math.sin(a0);
    const x1 = c + rArco * Math.cos(a1), y1 = c + rArco * Math.sin(a1);
    s += `<path d="M${x0.toFixed(2)} ${y0.toFixed(2)} A${rArco} ${rArco} 0 ${a1 - a0 > Math.PI ? 1 : 0} 1 ${x1.toFixed(2)} ${y1.toFixed(2)}" fill="none" style="stroke:${cor[p.no] || "var(--linha-forte)"}" stroke-width="${traco}"/>`;
  }
  if (marca !== undefined) {
    const a = marca * 2 * Math.PI - Math.PI / 2;
    const comp = rArco + traco / 2 + 4;
    const x = c + comp * Math.cos(a), y = c + comp * Math.sin(a);
    const xb = c - 18 * Math.cos(a), yb = c - 18 * Math.sin(a);
    s += `<line class="agulha-halo" x1="${xb.toFixed(2)}" y1="${yb.toFixed(2)}" x2="${x.toFixed(2)}" y2="${y.toFixed(2)}"/>`;
    s += `<line class="agulha" x1="${xb.toFixed(2)}" y1="${yb.toFixed(2)}" x2="${x.toFixed(2)}" y2="${y.toFixed(2)}"/>`;
    const pa = a, w = 5;
    s += `<path class="agulha-ponta" d="M${(x + 7 * Math.cos(pa)).toFixed(2)} ${(y + 7 * Math.sin(pa)).toFixed(2)} L${(x + w * Math.cos(pa + Math.PI / 2)).toFixed(2)} ${(y + w * Math.sin(pa + Math.PI / 2)).toFixed(2)} L${(x + w * Math.cos(pa - Math.PI / 2)).toFixed(2)} ${(y + w * Math.sin(pa - Math.PI / 2)).toFixed(2)}Z"/>`;
    s += `<circle class="agulha-eixo" cx="${c}" cy="${c}" r="5"/>`;
    s += `<text class="rumo" x="${c}" y="${c + 34}" text-anchor="middle">rumo ${String(Math.round(marca * 360) % 360).padStart(3, "0")}°</text>`;
  } else {
    s += `<text class="centro-num" x="${c}" y="${c + (grande ? 2 : 1)}" text-anchor="middle" font-size="${grande ? 40 : 28}">${ids.length}</text>`;
    s += `<text class="centro-rot" x="${c}" y="${c + (grande ? 24 : 18)}" text-anchor="middle" font-size="${grande ? 12 : 10.5}">${ids.length === 1 ? "nó" : "nós"}, ${t.vnodes} vnodes cada</text>`;
  }
  svg.innerHTML = s;
}

function desenharAnel(marca) {
  const t = estado.topologia;
  mostrador($("a-anel"), 360, true, marca);
  const ids = idsNos();
  const max = Math.max(...ids.map((id) => t.participacao[id]), 0.0001);
  const ideal = ids.length ? 1 / ids.length : 0;
  $("a-info").textContent = `${t.replicas} cópias por chave, ideal ${fmt.pct(ideal)} por nó`;
  $("a-legenda").innerHTML = ids.map((id, i) => `<div class="legenda-item"><span class="legenda-cor" style="background:${corNo(i)}"></span><span class="nome">${escapar(id)}</span><span class="fatia-barra"><span style="width:${((t.participacao[id] / max) * 100).toFixed(1)}%;background:${corNo(i)}"></span></span><span class="d">${fmt.pct(t.participacao[id])}</span></div>`).join("")
    || `<p class="nota">Nenhum nó no anel ainda.</p>`;
}

async function localizar(ev) {
  if (ev) ev.preventDefault();
  const k = $("a-chave").value.trim();
  if (!k) { $("a-chave").focus(); return; }
  try {
    const r = await api("localizar?k=" + encodeURIComponent(k));
    estado.marcaAnel = r.posicao;
    desenharAnel(r.posicao);
    $("a-resultado").innerHTML = `<ol class="replicas">${r.replicas.map((m, i) => `<li><span class="papel${i === 0 ? " primaria" : ""}">${i === 0 ? "primária" : "réplica " + i}</span><span class="ponto ${m.estado}"></span><span class="mono">${escapar(m.id)}</span><span class="mono fraco">${escapar(m.endereco)}</span></li>`).join("")}</ol><p class="nota"><span class="mono">${escapar(k)}</span> fica a ${fmt.pct(r.posicao)} do início do anel.</p>`;
  } catch (e) {
    aviso(e.message, true);
  }
}

/* Chaves */
async function carregarChaves(reiniciar) {
  if (reiniciar) { estado.depois = ""; $("c-tabela").innerHTML = ""; }
  const prefixo = $("c-prefixo").value.trim();
  try {
    const r = await api(`chaves?prefixo=${encodeURIComponent(prefixo)}&depois=${encodeURIComponent(estado.depois)}&limite=50`);
    const linhas = r.itens.map((it) => `<tr data-chave="${escapar(it.chave)}" tabindex="0"><td class="mono chave-celula" title="${escapar(it.chave)}">${escapar(it.chave)}</td><td class="valor">${it.binario ? `<span class="fraco">binário, ${fmt.bytes(it.tamanho)}</span>` : escapar(it.valor)}</td><td class="d op">${fmt.bytes(it.tamanho)}</td><td class="d op">${fmt.data(it.gravado)}</td><td class="d">${fmt.ttl(it.expira)}</td></tr>`).join("");
    $("c-tabela").insertAdjacentHTML("beforeend", linhas);
    if (!$("c-tabela").querySelector("tr[data-chave]")) {
      $("c-tabela").innerHTML = prefixo
        ? `<tr><td colspan="5" class="vazio"><strong>Nenhuma chave começa com ${escapar(prefixo)}</strong>Limpe o prefixo para ver todas, ou crie uma em Nova chave.</td></tr>`
        : `<tr><td colspan="5" class="vazio"><strong>O cluster ainda não tem chaves</strong>Crie a primeira em Nova chave ou traga um arquivo em Importar.</td></tr>`;
    }
    if (r.itens.length) estado.depois = r.itens[r.itens.length - 1].chave;
    $("c-mais").hidden = !r.mais;
    const n = $("c-tabela").querySelectorAll("tr[data-chave]").length;
    $("c-total").textContent = fmt.num(n) + (n === 1 ? " chave carregada" : " chaves carregadas") + (r.mais ? ", há mais" : "");
  } catch (e) {
    aviso(e.message, true);
  }
}

function abrirGaveta(titulo, html) {
  abrirGaveta.antes = document.activeElement;
  $("gv-titulo").textContent = titulo;
  $("gv-corpo").innerHTML = html;
  $("gaveta").classList.add("aberta");
  $("gaveta").setAttribute("aria-hidden", "false");
  $("veu").hidden = false;
  setTimeout(() => $("gv-fechar").focus(), 30);
}

function fecharGaveta() {
  if (!$("gaveta").classList.contains("aberta")) return;
  $("gaveta").classList.remove("aberta");
  $("gaveta").setAttribute("aria-hidden", "true");
  $("veu").hidden = true;
  if (abrirGaveta.antes && abrirGaveta.antes.focus) abrirGaveta.antes.focus();
}

function realcarJSON(texto) {
  return escapar(texto).replace(/(&quot;(?:[^&]|&(?!quot;))*?&quot;)(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g, (m, str, dois, lit, num) => {
    if (str) return dois ? `<span class="j-chave">${str}</span>${dois}` : `<span class="j-texto">${str}</span>`;
    if (lit) return `<span class="j-lit">${lit}</span>`;
    return `<span class="j-num">${num}</span>`;
  });
}

async function copiar(texto, rotulo) {
  try {
    await navigator.clipboard.writeText(texto);
    aviso(rotulo || "Copiado");
  } catch (e) {
    aviso("Não foi possível copiar: o navegador bloqueou a área de transferência", true);
  }
}

async function verChave(k) {
  try {
    const r = await api("chave?k=" + encodeURIComponent(k));
    const it = r.item;
    let valor = it.binario ? `(${fmt.bytes(it.tamanho)} binários)` : it.valor;
    let json = false;
    try { if (!it.binario && /^[\[{]/.test(valor.trim())) { valor = JSON.stringify(JSON.parse(valor), null, 2); json = true; } } catch (e) {}
    abrirGaveta(k, `
      <div><div class="valor-cab"><span>${json ? "Valor (JSON formatado)" : "Valor"}</span>${it.binario ? "" : `<button class="botao pequeno" id="gv-copiar" type="button">Copiar valor</button>`}</div>
      <pre class="valor-bloco">${json ? realcarJSON(valor) : escapar(valor)}</pre></div>
      <dl class="pares">
        <dt>Tamanho</dt><dd>${fmt.bytes(it.tamanho)}</dd>
        <dt>Versão</dt><dd>${escapar(it.versao)}</dd>
        <dt>Gravado em</dt><dd>${fmt.data(it.gravado)}</dd>
        <dt>Expira em</dt><dd>${fmt.ttl(it.expira)}</dd>
        <dt>Réplicas</dt><dd>${r.replicas.map((m, i) => `<span class="ponto ${m.estado}"></span> ${escapar(m.id)}${i === 0 ? " (primária)" : ""}`).join("<br>")}</dd>
        <dt>Respostas</dt><dd>${r.respostas}, via ${escapar(r.coordenador)}</dd>
      </dl>
      <div class="acoes-gaveta"><button class="botao primario" id="gv-editar" type="button">Editar</button><button class="botao" id="gv-local" type="button">Ver no anel</button><button class="botao perigo" id="gv-apagar" type="button">Apagar</button></div>`);
    if ($("gv-copiar")) $("gv-copiar").onclick = () => copiar(it.valor, "Valor copiado");
    $("gv-editar").onclick = () => formularioChave(k, it.binario ? "" : it.valor, it.versao);
    $("gv-local").onclick = () => { fecharGaveta(); $("a-chave").value = k; location.hash = "#anel"; setTimeout(localizar, 50); };
    $("gv-apagar").onclick = () => confirmarApagar(k);
  } catch (e) {
    aviso(e.message, true);
  }
}

function confirmarApagar(k) {
  const b = $("gv-apagar");
  if (b.dataset.confirmar !== "1") {
    b.dataset.confirmar = "1";
    b.textContent = "Confirmar: apagar " + (k.length > 18 ? k.slice(0, 18) + "…" : k);
    setTimeout(() => { if (b.isConnected) { b.dataset.confirmar = ""; b.textContent = "Apagar"; } }, 4000);
    return;
  }
  api("chave?k=" + encodeURIComponent(k), { metodo: "DELETE" }).then(() => {
    fecharGaveta();
    aviso("Chave apagada");
    registrarEvento(`${k} apagada pelo console`, "aviso");
    carregarChaves(true);
  }).catch((e) => aviso(e.message, true));
}

function formularioChave(k, valor, versao) {
  abrirGaveta(k ? "Editar " + k : "Nova chave", `
    <form id="gv-form">
      <label class="campo-rotulo">Chave<input class="campo mono" id="gv-chave" value="${escapar(k || "")}" ${k ? "readonly" : ""} required spellcheck="false" placeholder="usuario:42"></label>
      <label class="campo-rotulo">Valor<textarea class="campo" id="gv-valor" spellcheck="false">${escapar(valor || "")}</textarea></label>
      <label class="campo-rotulo">Expira em <small>opcional, por exemplo 30s, 15m ou 2h</small><input class="campo mono" id="gv-ttl" placeholder="sem expiração"></label>
      ${versao ? `<label class="marcar"><input type="checkbox" id="gv-cas" checked><span>Só gravar se ninguém alterou a chave desde a versão <span class="mono">${escapar(versao)}</span></span></label>` : ""}
      <div class="acoes-gaveta"><button class="botao primario" type="submit">Gravar</button><button class="botao" type="button" id="gv-cancelar">Cancelar</button></div>
    </form>`);
  $("gv-cancelar").onclick = fecharGaveta;
  $("gv-form").onsubmit = async (ev) => {
    ev.preventDefault();
    const corpo = { chave: $("gv-chave").value.trim(), valor: $("gv-valor").value, ttl: $("gv-ttl").value.trim() };
    if (versao && $("gv-cas") && $("gv-cas").checked) corpo.se_versao = versao;
    try {
      const r = await api("chave", { metodo: "PUT", corpo });
      aviso(`Gravado em ${r.confirmacoes} de ${r.replicas} réplicas`);
      registrarEvento(`${corpo.chave} gravada pelo console (${r.confirmacoes}/${r.replicas} réplicas)`, "ok");
      fecharGaveta();
      carregarChaves(true);
    } catch (e) {
      aviso(e.message, true);
    }
  };
  setTimeout(() => (k ? $("gv-valor") : $("gv-chave")).focus(), 60);
}

async function exportar() {
  const prefixo = $("c-prefixo").value.trim();
  const formato = $("c-formato").value;
  const b = $("c-exportar");
  b.disabled = true;
  try {
    const r = await api(`exportar?prefixo=${encodeURIComponent(prefixo)}&formato=${formato}`, { blob: true });
    const nome = ((r.headers.get("Content-Disposition") || "").match(/filename="([^"]+)"/) || [])[1] || "leme." + formato;
    const blob = await r.blob();
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = nome;
    document.body.appendChild(a);
    a.click();
    setTimeout(() => { URL.revokeObjectURL(a.href); a.remove(); }, 1000);
    aviso(`Exportado: ${nome}`);
    registrarEvento(`Exportação ${prefixo ? "de " + prefixo : "completa"} em ${formato.toUpperCase()} (${fmt.bytes(blob.size)})`, "ok");
  } catch (e) {
    aviso(e.message, true);
  } finally {
    b.disabled = false;
  }
}

function escolherImportacao(arquivo) {
  if (!arquivo) return;
  estado.importacao = arquivo;
  const ext = (arquivo.name.split(".").pop() || "").toLowerCase();
  const formato = ext === "csv" ? "csv" : ext === "json" ? "json" : "ndjson";
  abrirGaveta("Importar chaves", `
    <form id="im-form">
      <dl class="pares"><dt>Arquivo</dt><dd>${escapar(arquivo.name)}</dd><dt>Tamanho</dt><dd>${fmt.bytes(arquivo.size)}</dd></dl>
      <label class="campo-rotulo">Formato<select class="campo" id="im-formato"><option value="ndjson">NDJSON, um objeto por linha</option><option value="json">JSON, uma lista</option><option value="csv">CSV com cabeçalho</option></select></label>
      <label class="campo-rotulo">Prefixo de destino <small>opcional, acrescentado antes de cada chave</small><input class="campo mono" id="im-prefixo" placeholder="restaurado:" spellcheck="false"></label>
      <label class="marcar"><input type="checkbox" id="im-ausente"><span>Não sobrescrever chaves que já existem</span></label>
      <label class="marcar"><input type="checkbox" id="im-simular"><span>Só simular: valida o arquivo e conta o que seria gravado</span></label>
      <div class="acoes-gaveta"><button class="botao primario" type="submit" id="im-enviar">Importar</button><button class="botao" type="button" id="im-cancelar">Cancelar</button></div>
      <div id="im-resultado" aria-live="polite"></div>
    </form>`);
  $("im-formato").value = formato;
  $("im-cancelar").onclick = fecharGaveta;
  $("im-form").onsubmit = async (ev) => {
    ev.preventDefault();
    const b = $("im-enviar");
    b.disabled = true;
    const q = new URLSearchParams({ formato: $("im-formato").value, nome: arquivo.name, prefixo: $("im-prefixo").value.trim(), se_ausente: $("im-ausente").checked ? "1" : "", simular: $("im-simular").checked ? "1" : "" });
    try {
      const r = await api("importar?" + q.toString(), { metodo: "POST", bruto: arquivo });
      const rel = r.relatorio || {};
      $("im-resultado").innerHTML = `<div class="relatorio"><div><span>Lidas</span><strong>${fmt.num(rel.lidos)}</strong></div><div><span>${r.simulado ? "Seriam gravadas" : "Gravadas"}</span><strong>${fmt.num(rel.gravados)}</strong></div><div><span>Ignoradas</span><strong>${fmt.num(rel.ignorados)}</strong></div><div><span>Falhas</span><strong>${fmt.num(rel.falhas)}</strong></div></div>${(rel.erros || []).length ? `<ul class="lista-erros">${rel.erros.slice(0, 12).map((e) => `<li>${escapar(e)}</li>`).join("")}</ul>` : ""}`;
      if (!r.simulado) {
        registrarEvento(`Importação de ${arquivo.name}: ${fmt.num(rel.gravados)} gravadas, ${fmt.num(rel.falhas)} falhas`, rel.falhas ? "aviso" : "ok");
        carregarChaves(true);
      }
      aviso(r.simulado ? "Simulação concluída" : "Importação concluída");
    } catch (e) {
      aviso(e.message, true);
    } finally {
      b.disabled = false;
    }
  };
}

/* Diagnóstico */
async function diagnosticar() {
  const b = $("d-rodar");
  b.disabled = true;
  if (!$("d-lista").children.length) $("d-lista").innerHTML = `<li><span class="carregando">Rodando as verificações…</span></li>`;
  try {
    const r = await api("diagnostico");
    const vs = r.verificacoes || [];
    const c = r.contagem || {};
    const pior = c.falha ? "falha" : c.aviso ? "aviso" : "ok";
    $("d-ponto").className = "ponto " + pior;
    const partes = [`${vs.length} verificações`, `${c.ok || 0} sem problemas`];
    if (c.aviso) partes.push(`${c.aviso} ${c.aviso === 1 ? "aviso" : "avisos"}`);
    if (c.falha) partes.push(`${c.falha} ${c.falha === 1 ? "falha" : "falhas"}`);
    $("d-frase-texto").textContent = partes.join(", ") + ` (às ${fmt.hora(r.agora)}).`;
    const sinal = { ok: "✓", aviso: "!", falha: "×" };
    $("d-lista").innerHTML = vs.map((v) => `<li><span class="selo selo-${escapar(v.estado)}" aria-label="${escapar(v.estado)}">${sinal[v.estado] || "?"}</span><span class="nome">${escapar(v.nome)}</span><span class="detalhe">${escapar(v.detalhe)}</span>${v.sugestao ? `<span class="sugestao">${escapar(v.sugestao)}</span>` : ""}</li>`).join("")
      || `<li><span class="carregando">Nenhuma verificação foi retornada.</span></li>`;
  } catch (e) {
    $("d-ponto").className = "ponto erro";
    $("d-frase-texto").textContent = "Não foi possível rodar o diagnóstico: " + e.message;
    $("d-lista").innerHTML = "";
  } finally {
    b.disabled = false;
  }
}

/* Integrações */
const rotas = [
  ["GET", "/api/v1/saude", "Saúde do console, sem autenticação"],
  ["GET", "/api/v1/resumo", "Resumo do cluster e estatísticas por nó"],
  ["GET", "/api/v1/topologia", "Membros, réplicas e pontos do anel"],
  ["GET", "/api/v1/chaves?prefixo=", "Lista paginada de chaves"],
  ["GET", "/api/v1/chave?k=", "Lê uma chave com réplicas e versão"],
  ["PUT", "/api/v1/chave", "Grava; aceita ttl e se_versao"],
  ["DELETE", "/api/v1/chave?k=", "Apaga uma chave"],
  ["GET", "/api/v1/localizar?k=", "Réplicas e posição de uma chave"],
  ["GET", "/api/v1/exportar?prefixo=&formato=", "Baixa NDJSON, JSON ou CSV"],
  ["POST", "/api/v1/importar?formato=", "Restaura um arquivo exportado"],
  ["GET", "/api/v1/diagnostico", "Verificações do leme doctor"],
  ["POST", "/api/v1/executar", "Roda um comando da CLI"],
];

function trechos(url, token) {
  const t = token || "$LEME_TOKEN_CONSOLE";
  const tJs = token ? `"${token}"` : "process.env.LEME_TOKEN_CONSOLE";
  const tPy = token ? `"${token}"` : 'os.environ["LEME_TOKEN_CONSOLE"]';
  const tGo = token ? `"${token}"` : 'os.Getenv("LEME_TOKEN_CONSOLE")';
  return {
    curl: `# grava com TTL de 30 minutos
curl -X PUT ${url}/api/v1/chave \\
  -H "Authorization: Bearer ${t}" \\
  -H "Content-Type: application/json" \\
  -d '{"chave":"sessao:9f3","valor":"{\\"usuario\\":42}","ttl":"30m"}'

# lê
curl -H "Authorization: Bearer ${t}" \\
  "${url}/api/v1/chave?k=sessao:9f3"

# backup de um prefixo
curl -H "Authorization: Bearer ${t}" \\
  "${url}/api/v1/exportar?prefixo=usuario:&formato=ndjson" \\
  -o usuarios.ndjson`,
    javascript: `const LEME = "${url}/api/v1";
const cabecalhos = { Authorization: \`Bearer \${${tJs}}\` };

await fetch(\`\${LEME}/chave\`, {
  method: "PUT",
  headers: { ...cabecalhos, "Content-Type": "application/json" },
  body: JSON.stringify({
    chave: "sessao:9f3",
    valor: '{"usuario":42}',
    ttl: "30m",
  }),
});

const k = encodeURIComponent("sessao:9f3");
const r = await fetch(\`\${LEME}/chave?k=\${k}\`, { headers: cabecalhos });
const { item, replicas } = await r.json();
console.log(item.valor, item.versao, replicas.length);`,
    python: `import os, requests

LEME = "${url}/api/v1"
s = requests.Session()
s.headers["Authorization"] = f"Bearer {${tPy}}"

s.put(f"{LEME}/chave", json={
    "chave": "sessao:9f3",
    "valor": '{"usuario":42}',
    "ttl": "30m",
}).raise_for_status()

r = s.get(f"{LEME}/chave", params={"k": "sessao:9f3"})
r.raise_for_status()
item = r.json()["item"]
print(item["valor"], item["versao"])`,
    go: `package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const leme = "${url}/api/v1"

func main() {
	token := ${tGo}
	corpo := strings.NewReader(\`{"chave":"sessao:9f3","valor":"{\\"usuario\\":42}","ttl":"30m"}\`)
	req, _ := http.NewRequest(http.MethodPut, leme+"/chave", corpo)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if _, err := http.DefaultClient.Do(req); err != nil {
		panic(err)
	}

	req, _ = http.NewRequest(http.MethodGet, leme+"/chave?k="+url.QueryEscape("sessao:9f3"), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	fmt.Println(string(b))
}`,
  };
}
const nomesAbas = { curl: "curl", javascript: "JavaScript", python: "Python", go: "Go" };

const integracoes = [
  ["Prometheus", "Cada lemed publica /saude e /metricas quando sobe com --metricas. A configuração de coleta e as regras de alerta estão prontas.", ["deploy/prometheus/prometheus.yml", "deploy/prometheus/alertas.yml"]],
  ["Grafana", "Painel com operações por segundo, latência p99 por operação, disco, dicas pendentes e nós no ar. Importe o JSON e escolha a fonte Prometheus.", ["deploy/grafana/leme-dashboard.json"]],
  ["Kubernetes", "StatefulSet com três nós, serviço headless para o gossip, volume por nó e token em Secret.", ["deploy/kubernetes/leme.yaml", "kubectl apply -f deploy/kubernetes/leme.yaml"]],
  ["systemd", "Unidade para rodar o lemed como serviço, com usuário próprio, reinício automático e variáveis em /etc/leme/lemed.env.", ["deploy/systemd/lemed.service"]],
];

async function carregarIntegracao() {
  if (!estado.integracao) {
    try { estado.integracao = await api("integracao"); } catch (e) { estado.integracao = { url: location.origin, protegido: false, token: "" }; }
  }
  const i = estado.integracao;
  const url = i.url || location.origin;
  if (i.protegido) {
    const mascarado = i.token.slice(0, 3) + "•".repeat(Math.max(i.token.length - 6, 6)) + i.token.slice(-3);
    $("i-token").innerHTML = `<span>Token do console</span><code id="i-token-valor">${escapar(estado.mostrarToken ? i.token : mascarado)}</code><button class="botao pequeno" id="i-mostrar" type="button">${estado.mostrarToken ? "Ocultar" : "Mostrar"}</button><button class="botao pequeno" id="i-copiar-token" type="button">Copiar token</button>`;
    $("i-mostrar").onclick = () => { estado.mostrarToken = !estado.mostrarToken; carregarIntegracao(); };
    $("i-copiar-token").onclick = () => copiar(i.token, "Token copiado");
  } else {
    $("i-token").innerHTML = `<span>Este console escuta só em 127.0.0.1 e não exige token. Para expô-lo, rode <code>leme console --endereco 0.0.0.0:8080 --token-console ...</code>.</span>`;
  }
  $("i-rotas").innerHTML = rotas.map((r) => `<tr><td><span class="metodo ${r[0].toLowerCase()}">${r[0]}</span></td><td class="mono">${escapar(r[1])}</td><td class="fraco">${escapar(r[2])}</td></tr>`).join("");
  const tr = trechos(url, i.protegido && estado.mostrarToken ? i.token : "");
  $("i-abas").innerHTML = Object.keys(tr).map((k) => `<button type="button" role="tab" aria-selected="${k === estado.aba}" data-aba="${k}">${nomesAbas[k]}</button>`).join("");
  $("i-trecho").textContent = tr[estado.aba];
  $("i-abas").onclick = (ev) => { const b = ev.target.closest("[data-aba]"); if (b) { estado.aba = b.dataset.aba; carregarIntegracao(); } };
  $("i-copiar").onclick = () => copiar(tr[estado.aba], "Exemplo copiado");
  $("i-lista").innerHTML = integracoes.map((x) => `<div class="integracao"><h3>${x[0]}</h3><p>${escapar(x[1])}</p>${x[2].map((c) => `<code>${escapar(c)}</code>`).join("<br>")}</div>`).join("");
}

/* Terminal */
function terminalEscrever(cmd, saida, erro, tempo) {
  const b = document.createElement("div");
  b.className = "t-bloco";
  const add = (cls, txt) => { const d = document.createElement("div"); d.className = cls; d.textContent = txt; b.appendChild(d); };
  if (cmd !== null) add("t-comando", cmd);
  if (saida) add("t-resultado", saida.replace(/\n$/, ""));
  if (erro) add("t-erro", "erro: " + erro);
  if (tempo !== undefined) add("t-tempo", fmt.ms(tempo) + " ms");
  $("t-saida").appendChild(b);
  $("t-saida").scrollTop = $("t-saida").scrollHeight;
}

async function terminalRodar(linha) {
  if (estado.historico[estado.historico.length - 1] !== linha) estado.historico.push(linha);
  estado.posHist = estado.historico.length;
  if (linha === "limpar" || linha === "clear") { $("t-saida").innerHTML = ""; return; }
  try {
    const r = await api("executar", { metodo: "POST", corpo: { linha } });
    terminalEscrever(linha, r.saida, r.erro, r.duracao_ms);
  } catch (e) {
    terminalEscrever(linha, "", e.message);
  }
}

function terminalEnviar(ev) {
  ev.preventDefault();
  const linha = $("t-entrada").value.trim();
  $("t-entrada").value = "";
  if (linha) terminalRodar(linha);
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

/* Paleta de comandos */
const paleta = { itens: [], sel: 0, chaves: [], pedido: 0, comandos: [] };

function itensPaleta(q) {
  const termo = q.trim();
  const baixo = termo.toLowerCase();
  const casa = (t) => !baixo || t.toLowerCase().includes(baixo);
  const lista = [];
  for (const [id, p] of Object.entries(paginas)) if (casa(p[0]) || casa(id)) lista.push({ secao: "Páginas", rotulo: p[0], tipo: "g " + p[2], fazer: () => (location.hash = "#" + id) });
  const acoes = [
    ["Nova chave", "n", () => { location.hash = "#chaves"; formularioChave("", "", ""); }],
    ["Exportar chaves do prefixo atual", "", () => { location.hash = "#chaves"; exportar(); }],
    ["Importar arquivo de chaves", "", () => { location.hash = "#chaves"; $("c-arquivo").click(); }],
    ["Rodar o diagnóstico", "", () => { location.hash = "#diagnostico"; diagnosticar(); }],
    ["Alternar tema claro e escuro", "", alternarTema],
    ["Ver atalhos de teclado", "?", abrirAtalhos],
  ];
  for (const a of acoes) if (casa(a[0])) lista.push({ secao: "Ações", rotulo: a[0], tipo: a[1], fazer: a[2] });
  if (termo) {
    for (const k of paleta.chaves) lista.push({ secao: "Chaves", rotulo: k, mono: true, tipo: "abrir", fazer: () => { location.hash = "#chaves"; verChave(k); } });
    if (!paleta.chaves.includes(termo)) lista.push({ secao: "Chaves", rotulo: `Abrir a chave ${termo}`, tipo: "ler", fazer: () => { location.hash = "#chaves"; verChave(termo); } });
    const primeiro = baixo.split(/\s+/)[0];
    if (!paleta.comandos.length || paleta.comandos.includes(primeiro) || primeiro === "ajuda")
      lista.push({ secao: "Terminal", rotulo: termo, mono: true, tipo: "executar", fazer: () => { location.hash = "#terminal"; terminalRodar(termo); } });
  }
  return lista;
}

function desenharPaleta() {
  const q = $("paleta-entrada").value;
  paleta.itens = itensPaleta(q);
  paleta.sel = Math.min(paleta.sel, Math.max(paleta.itens.length - 1, 0));
  let secao = "";
  let html = "";
  paleta.itens.forEach((it, i) => {
    if (it.secao !== secao) { secao = it.secao; html += `<li class="secao" role="presentation">${secao}</li>`; }
    html += `<li role="option" id="po-${i}" data-i="${i}" aria-selected="${i === paleta.sel}"><span class="${it.mono ? "mono" : ""}">${escapar(it.rotulo)}</span>${it.tipo ? `<span class="tipo">${escapar(it.tipo)}</span>` : ""}</li>`;
  });
  $("paleta-lista").innerHTML = html || `<li class="nada">Nada encontrado. Digite um nome de chave ou um comando, como cluster status.</li>`;
  $("paleta-entrada").setAttribute("aria-activedescendant", paleta.itens.length ? "po-" + paleta.sel : "");
  const sel = $("po-" + paleta.sel);
  if (sel) sel.scrollIntoView({ block: "nearest" });
}

function buscarChavesPaleta() {
  const termo = $("paleta-entrada").value.trim();
  const n = ++paleta.pedido;
  if (!termo || termo.includes(" ")) { paleta.chaves = []; desenharPaleta(); return; }
  clearTimeout(buscarChavesPaleta.t);
  buscarChavesPaleta.t = setTimeout(async () => {
    try {
      const r = await api(`chaves?prefixo=${encodeURIComponent(termo)}&limite=6`);
      if (n !== paleta.pedido) return;
      paleta.chaves = r.itens.map((it) => it.chave);
    } catch (e) { paleta.chaves = []; }
    desenharPaleta();
  }, 140);
}

function abrirPaleta() {
  abrirPaleta.antes = document.activeElement;
  $("paleta-fundo").hidden = false;
  $("paleta-entrada").value = "";
  paleta.sel = 0;
  paleta.chaves = [];
  desenharPaleta();
  setTimeout(() => $("paleta-entrada").focus(), 10);
}

function fecharPaleta() {
  if ($("paleta-fundo").hidden) return false;
  $("paleta-fundo").hidden = true;
  if (abrirPaleta.antes && abrirPaleta.antes.focus) abrirPaleta.antes.focus();
  return true;
}

function escolherPaleta(i) {
  const it = paleta.itens[i];
  if (!it) return;
  $("paleta-fundo").hidden = true;
  it.fazer();
}

function abrirAtalhos() { $("atalhos-fundo").hidden = false; setTimeout(() => $("atalhos-fechar").focus(), 10); }
function fecharAtalhos() { if ($("atalhos-fundo").hidden) return false; $("atalhos-fundo").hidden = true; return true; }

function fecharMenu() { $("menu").classList.remove("aberto"); $("abrir-menu").setAttribute("aria-expanded", "false"); }

function alternarTema() {
  const escuro = document.documentElement.classList.toggle("dark");
  try { localStorage.setItem("leme-tema", escuro ? "escuro" : "claro"); } catch (e) {}
  desenharPagina();
}

function digitando(el) {
  return el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT" || el.isContentEditable);
}

function teclado(ev) {
  if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "k") {
    ev.preventDefault();
    if ($("paleta-fundo").hidden) abrirPaleta(); else fecharPaleta();
    return;
  }
  if (ev.key === "Escape") {
    if (fecharPaleta() || fecharAtalhos()) return;
    fecharGaveta();
    fecharMenu();
    return;
  }
  if (digitando(ev.target) || ev.ctrlKey || ev.metaKey || ev.altKey) return;
  if (teclado.g && Date.now() - teclado.g < 1200) {
    teclado.g = 0;
    const alvo = Object.entries(paginas).find((p) => p[1][2] === ev.key.toLowerCase());
    if (alvo) { location.hash = "#" + alvo[0]; ev.preventDefault(); }
    return;
  }
  if (ev.key === "g") { teclado.g = Date.now(); return; }
  if (ev.key === "/") { ev.preventDefault(); location.hash = "#chaves"; setTimeout(() => { $("c-prefixo").focus(); $("c-prefixo").select(); }, 30); return; }
  if (ev.key === "?") { ev.preventDefault(); abrirAtalhos(); return; }
  if (ev.key === "n") { ev.preventDefault(); location.hash = "#chaves"; formularioChave("", "", ""); }
}

function iniciar() {
  window.addEventListener("hashchange", navegar);
  document.addEventListener("keydown", teclado);
  $("tema").onclick = alternarTema;
  $("abrir-menu").onclick = () => { const a = $("menu").classList.toggle("aberto"); $("abrir-menu").setAttribute("aria-expanded", String(a)); };
  $("veu").onclick = fecharGaveta;
  $("gv-fechar").onclick = fecharGaveta;
  $("abrir-paleta").onclick = abrirPaleta;
  $("ver-atalhos").onclick = abrirAtalhos;
  $("atalhos-fechar").onclick = fecharAtalhos;
  $("atalhos-fundo").onclick = (ev) => { if (ev.target === $("atalhos-fundo")) fecharAtalhos(); };
  $("paleta-fundo").onclick = (ev) => { if (ev.target === $("paleta-fundo")) fecharPaleta(); };
  $("paleta-entrada").oninput = () => { paleta.sel = 0; desenharPaleta(); buscarChavesPaleta(); };
  $("paleta-entrada").onkeydown = (ev) => {
    if (ev.key === "ArrowDown") { paleta.sel = Math.min(paleta.sel + 1, paleta.itens.length - 1); desenharPaleta(); ev.preventDefault(); }
    else if (ev.key === "ArrowUp") { paleta.sel = Math.max(paleta.sel - 1, 0); desenharPaleta(); ev.preventDefault(); }
    else if (ev.key === "Enter") { ev.preventDefault(); escolherPaleta(paleta.sel); }
  };
  $("paleta-lista").onclick = (ev) => { const li = ev.target.closest("[data-i]"); if (li) escolherPaleta(+li.dataset.i); };
  $("a-form").onsubmit = localizar;
  $("c-form").onsubmit = (ev) => { ev.preventDefault(); carregarChaves(true); };
  $("c-mais").onclick = () => carregarChaves(false);
  $("c-nova").onclick = () => formularioChave("", "", "");
  $("c-exportar").onclick = exportar;
  $("c-importar").onclick = () => { $("c-arquivo").value = ""; $("c-arquivo").click(); };
  $("c-arquivo").onchange = () => escolherImportacao($("c-arquivo").files[0]);
  $("c-tabela").onclick = (ev) => { const tr = ev.target.closest("tr[data-chave]"); if (tr) verChave(tr.dataset.chave); };
  $("c-tabela").onkeydown = (ev) => { const tr = ev.target.closest("tr[data-chave]"); if (tr && (ev.key === "Enter" || ev.key === " ")) { ev.preventDefault(); verChave(tr.dataset.chave); } };
  $("d-rodar").onclick = diagnosticar;
  $("t-form").onsubmit = terminalEnviar;
  $("t-entrada").onkeydown = terminalTeclas;
  $("t-terminal").onclick = () => { if (!window.getSelection().toString()) $("t-entrada").focus(); };
  terminalEscrever(null, "Terminal web do Leme. Digite ajuda para ver os comandos.\nExemplos: put usuario:1 '{\"plano\":\"anual\"}' --ttl 2h   get usuario:1 --detalhes   incr contador:visitas   doctor");
  api("comandos").then((r) => { paleta.comandos = (r.comandos || []).map((c) => String(c).toLowerCase().split(" ")[0]); }).catch(() => {});
  navegar();
  atualizar();
  estado.timer = setInterval(atualizar, 2500);
}

iniciar();
