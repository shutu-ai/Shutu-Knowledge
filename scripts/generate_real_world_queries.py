import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
out = root / "benchmarks/real_world_internalization/queries.jsonl"
out.parent.mkdir(parents=True, exist_ok=True)

A = "open5gs"
B = "3gpp"

a_fact = [
    ("What is Open5GS?", [f"{A}/docs/_pages/home.md", f"{A}/docs/_pages/features.md"], ["Open5GS"]),
    ("Which platforms are documented for Open5GS installation?", [f"{A}/docs/_docs/platform/01-debian-ubuntu.md", f"{A}/docs/_docs/platform/02-centos.md", f"{A}/docs/_docs/platform/03-fedora.md"], ["Debian", "CentOS", "Fedora"]),
    ("What is required to build Open5GS from source?", [f"{A}/docs/_docs/guide/02-building-open5gs-from-sources.md"], ["source", "build"]),
    ("What does the first LTE tutorial cover?", [f"{A}/docs/_docs/tutorial/01-your-first-lte.md"], ["LTE", "tutorial"]),
    ("What is 5G EIR?", [f"{A}/docs/_docs/guide/03-5g-eir.md"], ["EIR"]),
    ("Which tutorial covers roaming?", [f"{A}/docs/_docs/tutorial/05-roaming.md"], ["roaming"]),
    ("What does the AMF API tutorial cover?", [f"{A}/docs/_docs/tutorial/08-AMF-API-addAndDelete-new-PLMN.md"], ["AMF", "API"]),
    ("What metrics does Open5GS document for Prometheus?", [f"{A}/docs/_docs/tutorial/04-metrics-prometheus.md"], ["Prometheus", "metrics"]),
    ("What is Genode base station documentation about?", [f"{A}/docs/_docs/hardware/01-genodebs.md"], ["Genode", "base station"]),
    ("What is the Open5GS documentation index?", [f"{A}/docs/_pages/docs.md"], ["documentation", "index"]),
]
a_local = [
    ("What is the structure of the Open5GS quickstart guide?", [f"{A}/docs/_docs/guide/01-quickstart.md"], ["quickstart"]),
    ("What is covered by building Open5GS from sources?", [f"{A}/docs/_docs/guide/02-building-open5gs-from-sources.md"], ["building", "source"]),
    ("What is covered by the 5G EIR guide?", [f"{A}/docs/_docs/guide/03-5g-eir.md"], ["EIR"]),
    ("What is covered by the roaming tutorial?", [f"{A}/docs/_docs/tutorial/05-roaming.md"], ["roaming"]),
]
a_global = [
    ("Summarize the whole Open5GS corpus.", [f"{A}/docs/_pages/home.md", f"{A}/docs/_pages/features.md", f"{A}/docs/_docs/guide/01-quickstart.md"], ["Open5GS", "architecture"]),
    ("What capability domains does Open5GS cover?", [f"{A}/docs/_pages/features.md", f"{A}/docs/_pages/home.md"], ["features", "capability"]),
    ("What deployment platforms does Open5GS document?", [f"{A}/docs/_docs/platform/01-debian-ubuntu.md", f"{A}/docs/_docs/platform/02-centos.md", f"{A}/docs/_docs/platform/03-fedora.md"], ["platform"]),
    ("What operational workflows are documented in Open5GS?", [f"{A}/docs/_docs/troubleshoot/01-simple-issues.md", f"{A}/docs/_docs/tutorial/04-metrics-prometheus.md"], ["operations", "troubleshooting"]),
]
a_cross = [
    ("How do quickstart and building from source relate?", [f"{A}/docs/_docs/guide/01-quickstart.md", f"{A}/docs/_docs/guide/02-building-open5gs-from-sources.md"], ["quickstart", "source"]),
    ("How does the roaming tutorial relate to platform guides?", [f"{A}/docs/_docs/tutorial/05-roaming.md", f"{A}/docs/_docs/platform/01-debian-ubuntu.md"], ["roaming", "platform"]),
    ("How does the AMF API relate to tutorial session data?", [f"{A}/docs/_docs/tutorial/08-AMF-API-addAndDelete-new-PLMN.md", f"{A}/docs/_docs/tutorial/07-infoAPI-UE-gNB-session-data.md"], ["AMF", "session"]),
    ("How do troubleshooting docs relate to quickstart?", [f"{A}/docs/_docs/troubleshoot/01-simple-issues.md", f"{A}/docs/_docs/guide/01-quickstart.md"], ["troubleshooting", "quickstart"]),
    ("How does the metrics tutorial relate to platform guides?", [f"{A}/docs/_docs/tutorial/04-metrics-prometheus.md", f"{A}/docs/_docs/platform/01-debian-ubuntu.md"], ["metrics", "platform"]),
]
a_hop = [
    ("How does quickstart lead to VoLTE setup?", [f"{A}/docs/_docs/guide/01-quickstart.md", f"{A}/docs/_docs/tutorial/02-VoLTE-setup.md"], ["quickstart", "VoLTE"]),
    ("How does building from source support platform guides?", [f"{A}/docs/_docs/guide/02-building-open5gs-from-sources.md", f"{A}/docs/_docs/platform/01-debian-ubuntu.md"], ["source", "platform"]),
    ("How does 5G EIR relate to the first LTE tutorial?", [f"{A}/docs/_docs/guide/03-5g-eir.md", f"{A}/docs/_docs/tutorial/01-your-first-lte.md"], ["EIR", "LTE"]),
    ("How does the roaming tutorial use AMF API?", [f"{A}/docs/_docs/tutorial/05-roaming.md", f"{A}/docs/_docs/tutorial/08-AMF-API-addAndDelete-new-PLMN.md"], ["roaming", "AMF"]),
    ("How does Open5GS orchestrator use tutorial session data?", [f"{A}/docs/_docs/tutorial/06-Open5GS-with-5G-Sharp-Orchestrator.md", f"{A}/docs/_docs/tutorial/07-infoAPI-UE-gNB-session-data.md"], ["orchestrator", "session"]),
]
a_temp = [
    ("What changed in Open5GS release v2.8.0?", [f"{A}/docs/_posts/2026-06-20-release-v2.8.0.md"], ["2.8.0"]),
    ("What changed in Open5GS release v2.7.6?", [f"{A}/docs/_posts/2025-07-19-release-v2.7.6.md"], ["2.7.6"]),
    ("What changed in Open5GS release v2.7.5?", [f"{A}/docs/_posts/2025-03-30-release-v2.7.5.md"], ["2.7.5"]),
    ("What changed in Open5GS release v2.7.0?", [f"{A}/docs/_posts/2023-12-04-release-v2.7.0.md"], ["2.7.0"]),
]
a_comp = [
    ("Compare building from source and platform installation.", [f"{A}/docs/_docs/guide/02-building-open5gs-from-sources.md", f"{A}/docs/_docs/platform/01-debian-ubuntu.md"], ["source", "platform"]),
    ("Compare LTE and VoLTE tutorials.", [f"{A}/docs/_docs/tutorial/01-your-first-lte.md", f"{A}/docs/_docs/tutorial/02-VoLTE-setup.md"], ["LTE", "VoLTE"]),
    ("Compare roaming and first LTE tutorials.", [f"{A}/docs/_docs/tutorial/05-roaming.md", f"{A}/docs/_docs/tutorial/01-your-first-lte.md"], ["roaming", "LTE"]),
    ("Compare 5G EIR and first LTE tutorial.", [f"{A}/docs/_docs/guide/03-5g-eir.md", f"{A}/docs/_docs/tutorial/01-your-first-lte.md"], ["EIR", "LTE"]),
]
a_expl = [
    ("Why does Open5GS document platform-specific installation?", [f"{A}/docs/_docs/platform/01-debian-ubuntu.md", f"{A}/docs/_docs/platform/02-centos.md"], ["platform", "installation"]),
    ("Why is roaming a separate tutorial?", [f"{A}/docs/_docs/tutorial/05-roaming.md"], ["roaming"]),
    ("Why does quickstart emphasize first LTE?", [f"{A}/docs/_docs/guide/01-quickstart.md"], ["quickstart", "LTE"]),
]

b_fact = [
    ("What is TS 23.501 about?", [f"{B}/ts_123501v170700p.pdf"], ["5G", "system architecture"]),
    ("What is TS 23.502 about?", [f"{B}/ts_123502v170700p.pdf"], ["5G", "procedures"]),
    ("Which spec covers 5G system architecture?", [f"{B}/ts_123501v170700p.pdf"], ["architecture"]),
    ("Which spec covers procedures?", [f"{B}/ts_123502v170700p.pdf"], ["procedures"]),
    ("What are the main network functions in 5G?", [f"{B}/ts_123501v170700p.pdf"], ["network function", "AMF", "SMF", "UPF"]),
    ("What is AMF?", [f"{B}/ts_123501v170700p.pdf"], ["AMF"]),
    ("What is SMF?", [f"{B}/ts_123501v170700p.pdf"], ["SMF"]),
    ("What is UPF?", [f"{B}/ts_123501v170700p.pdf"], ["UPF"]),
    ("What is registration management?", [f"{B}/ts_123502v170700p.pdf"], ["registration"]),
    ("What is session management?", [f"{B}/ts_123502v170700p.pdf"], ["session management"]),
]
b_local = [
    ("What is the structure of TS 23.501?", [f"{B}/ts_123501v170700p.pdf"], ["structure", "architecture"]),
    ("What is the structure of TS 23.502?", [f"{B}/ts_123502v170700p.pdf"], ["structure", "procedures"]),
    ("What section covers architecture in 23.501?", [f"{B}/ts_123501v170700p.pdf"], ["architecture"]),
    ("What section covers procedures in 23.502?", [f"{B}/ts_123502v170700p.pdf"], ["procedures"]),
]
b_global = [
    ("Summarize the 3GPP 5G system architecture.", [f"{B}/ts_123501v170700p.pdf"], ["architecture", "network function"]),
    ("What capability domains are covered in the 5G system?", [f"{B}/ts_123501v170700p.pdf"], ["capability", "domain"]),
    ("What architecture principles are documented in 23.501?", [f"{B}/ts_123501v170700p.pdf"], ["architecture", "principle"]),
    ("What procedures are documented in 23.502?", [f"{B}/ts_123502v170700p.pdf"], ["procedure"]),
]
b_cross = [
    ("How do TS 23.501 and TS 23.502 relate?", [f"{B}/ts_123501v170700p.pdf", f"{B}/ts_123502v170700p.pdf"], ["architecture", "procedures"]),
    ("How does architecture relate to procedures?", [f"{B}/ts_123501v170700p.pdf", f"{B}/ts_123502v170700p.pdf"], ["architecture", "procedure"]),
    ("How does AMF relate to registration?", [f"{B}/ts_123501v170700p.pdf", f"{B}/ts_123502v170700p.pdf"], ["AMF", "registration"]),
    ("How does SMF relate to session management?", [f"{B}/ts_123501v170700p.pdf", f"{B}/ts_123502v170700p.pdf"], ["SMF", "session management"]),
    ("How does UPF relate to data transport?", [f"{B}/ts_123501v170700p.pdf", f"{B}/ts_123502v170700p.pdf"], ["UPF", "data transport"]),
]
b_hop = [
    ("How does UE registration involve AMF and SMF?", [f"{B}/ts_123502v170700p.pdf"], ["UE", "AMF", "SMF"]),
    ("How does session establishment involve SMF and UPF?", [f"{B}/ts_123502v170700p.pdf"], ["session establishment", "SMF", "UPF"]),
    ("How does mobility management involve AMF and UPF?", [f"{B}/ts_123502v170700p.pdf"], ["mobility", "AMF", "UPF"]),
    ("How does architecture concept lead to procedure?", [f"{B}/ts_123501v170700p.pdf", f"{B}/ts_123502v170700p.pdf"], ["architecture", "procedure"]),
    ("How does network function interaction support session management?", [f"{B}/ts_123501v170700p.pdf", f"{B}/ts_123502v170700p.pdf"], ["network function", "session management"]),
]
b_temp = [
    ("Which spec version is used?", [f"{B}/ts_123501v170700p.pdf"], ["17.7.0"]),
    ("What changed in 23.501?", [f"{B}/ts_123501v170700p.pdf"], ["change", "23.501"]),
    ("What changed in 23.502?", [f"{B}/ts_123502v170700p.pdf"], ["change", "23.502"]),
    ("Which release is referenced?", [f"{B}/ts_123501v170700p.pdf"], ["release"]),
]
b_comp = [
    ("Compare TS 23.501 architecture and TS 23.502 procedures.", [f"{B}/ts_123501v170700p.pdf", f"{B}/ts_123502v170700p.pdf"], ["architecture", "procedure"]),
    ("Compare registration and session management.", [f"{B}/ts_123502v170700p.pdf"], ["registration", "session management"]),
    ("Compare control-plane and user-plane functions.", [f"{B}/ts_123501v170700p.pdf"], ["control-plane", "user-plane"]),
    ("Compare AMF and SMF responsibilities.", [f"{B}/ts_123501v170700p.pdf"], ["AMF", "SMF"]),
]
b_expl = [
    ("Why does the 5G system separate control and user plane?", [f"{B}/ts_123501v170700p.pdf"], ["control-plane", "user-plane"]),
    ("Why is registration management needed?", [f"{B}/ts_123502v170700p.pdf"], ["registration"]),
    ("Why are procedures documented separately?", [f"{B}/ts_123502v170700p.pdf"], ["procedure"]),
    ("Why is architecture modular?", [f"{B}/ts_123501v170700p.pdf"], ["modular"]),
]

c_fact = [
    ("What is Shutu Knowledge?", ["README.md"], ["Shutu Knowledge"]),
    ("What is the 0.4 knowledge model?", ["docs/0.4_knowledge_model.md"], ["KnowledgeUnit", "model"]),
    ("What is semantic compilation?", ["docs/0.4_knowledge_compiler.md"], ["compilation"]),
    ("What is query routing?", ["docs/0.4_query_routing.md"], ["routing"]),
    ("What is context compiler?", ["docs/0.4_context_compiler.md"], ["ContextPackage"]),
    ("What is Living Wiki?", ["docs/0.4_living_wiki.md"], ["Wiki"]),
    ("What is evidence provenance?", ["docs/0.4_knowledge_model.md"], ["provenance"]),
    ("What is incremental compilation?", ["docs/0.4_incremental_compilation.md"], ["incremental"]),
    ("What is delete propagation?", ["docs/0.4_incremental_compilation.md"], ["delete"]),
    ("What is semantic memory?", ["docs/0.4_semantic_memory.md"], ["semantic memory"]),
]
c_local = [
    ("What does the 0.4 knowledge model doc cover?", ["docs/0.4_knowledge_model.md"], ["KnowledgeUnit"]),
    ("What does the 0.4 compiler doc cover?", ["docs/0.4_knowledge_compiler.md"], ["compiler"]),
    ("What does the incremental compilation doc cover?", ["docs/0.4_incremental_compilation.md"], ["incremental"]),
    ("What does semantic memory doc cover?", ["docs/0.4_semantic_memory.md"], ["semantic"]),
]
c_global = [
    ("Summarize the whole 0.4 knowledge architecture.", ["docs/0.4_knowledge_model.md", "docs/0.4_knowledge_compiler.md", "docs/0.4_context_compiler.md"], ["knowledge", "architecture"]),
    ("What capability domains are covered in 0.4?", ["docs/0.4_knowledge_model.md", "docs/0.4_semantic_memory.md"], ["capability"]),
    ("What architecture principles are documented?", ["docs/architecture.md", "docs/0.4_knowledge_model.md"], ["architecture", "principle"]),
    ("What implementation modules are documented?", ["docs/0.4_knowledge_model.md", "docs/0.4_knowledge_compiler.md"], ["module"]),
]
c_cross = [
    ("How do model and compiler docs relate?", ["docs/0.4_knowledge_model.md", "docs/0.4_knowledge_compiler.md"], ["model", "compiler"]),
    ("How do compiler and incremental docs relate?", ["docs/0.4_knowledge_compiler.md", "docs/0.4_incremental_compilation.md"], ["compiler", "incremental"]),
    ("How do semantic memory and context compiler docs relate?", ["docs/0.4_semantic_memory.md", "docs/0.4_context_compiler.md"], ["semantic memory", "context"]),
    ("How do API and UI docs relate to semantic memory?", ["docs/0.4_api_ui.md", "docs/0.4_semantic_memory.md"], ["API", "semantic memory"]),
    ("How do lightweight relations relate to multi-hop?", ["docs/0.4_lightweight_relations.md", "docs/0.4_context_compiler.md"], ["relation", "multi-hop"]),
]
c_hop = [
    ("How does Document IR become semantic memory?", ["docs/document_ir_design.md", "docs/0.4_knowledge_model.md"], ["Document IR", "semantic"]),
    ("How does semantic memory become context?", ["docs/0.4_semantic_memory.md", "docs/0.4_context_compiler.md"], ["semantic memory", "context"]),
    ("How does routing affect context compiler?", ["docs/0.4_query_routing.md", "docs/0.4_context_compiler.md"], ["routing", "context"]),
    ("How does incremental compilation affect wiki?", ["docs/0.4_incremental_compilation.md", "docs/0.4_living_wiki.md"], ["incremental", "wiki"]),
    ("How does model provenance affect evidence?", ["docs/0.4_knowledge_model.md", "docs/0.4_semantic_memory.md"], ["provenance", "evidence"]),
]
c_temp = [
    ("What changed in v0.4.0?", ["docs/release_report_0.4.0.md"], ["0.4.0"]),
    ("What changed in the 0.4 knowledge model?", ["docs/0.4_knowledge_model.md"], ["0.4"]),
    ("What changed in the compiler?", ["docs/0.4_knowledge_compiler.md"], ["compiler"]),
    ("What changed in incremental compilation?", ["docs/0.4_incremental_compilation.md"], ["incremental"]),
]
c_comp = [
    ("Compare 0.3 evidence search and 0.4 semantic memory.", ["docs/0.4_semantic_memory.md"], ["0.3", "semantic memory"]),
    ("Compare Fact and Concept units.", ["docs/0.4_knowledge_model.md"], ["Fact", "Concept"]),
    ("Compare Topic and Summary units.", ["docs/0.4_knowledge_model.md"], ["Topic", "Summary"]),
    ("Compare semantic memory and Living Wiki.", ["docs/0.4_semantic_memory.md", "docs/0.4_living_wiki.md"], ["semantic memory", "Wiki"]),
]
c_expl = [
    ("Why does 0.4 preserve exact evidence?", ["docs/0.4_knowledge_model.md", "docs/0.4_context_compiler.md"], ["evidence", "provenance"]),
    ("Why is semantic compilation optional?", ["docs/0.4_semantic_memory.md"], ["optional"]),
    ("Why is Living Wiki not source of truth?", ["docs/0.4_living_wiki.md"], ["view"]),
    ("Why is incremental compilation needed?", ["docs/0.4_incremental_compilation.md"], ["incremental"]),
]

rows = []

def add(corpus, category, items):
    prefix = {"A": "A", "B": "B", "C": "C"}[corpus]
    tag = {"FACT": "FACT", "LOCAL": "LOCAL", "GLOBAL": "GLOBAL", "CROSS_DOCUMENT": "CROSS", "MULTI_HOP": "HOP", "TEMPORAL": "TEMP", "COMPARISON": "COMP", "EXPLANATION": "EXPL"}[category]
    for index, (query, docs, terms) in enumerate(items, 1):
        rows.append({
            "id": f"{prefix}-{tag}-{index:03d}",
            "corpus": corpus,
            "category": category,
            "query": query,
            "expectedDocs": docs,
            "expectedTerms": terms,
        })

for corpus, category, items in [
    ("A", "FACT", a_fact), ("A", "LOCAL", a_local), ("A", "GLOBAL", a_global), ("A", "CROSS_DOCUMENT", a_cross),
    ("A", "MULTI_HOP", a_hop), ("A", "TEMPORAL", a_temp), ("A", "COMPARISON", a_comp), ("A", "EXPLANATION", a_expl),
    ("B", "FACT", b_fact), ("B", "LOCAL", b_local), ("B", "GLOBAL", b_global), ("B", "CROSS_DOCUMENT", b_cross),
    ("B", "MULTI_HOP", b_hop), ("B", "TEMPORAL", b_temp), ("B", "COMPARISON", b_comp), ("B", "EXPLANATION", b_expl),
    ("C", "FACT", c_fact), ("C", "LOCAL", c_local), ("C", "GLOBAL", c_global), ("C", "CROSS_DOCUMENT", c_cross),
    ("C", "MULTI_HOP", c_hop), ("C", "TEMPORAL", c_temp), ("C", "COMPARISON", c_comp), ("C", "EXPLANATION", c_expl),
]:
    add(corpus, category, items)

with out.open("w", encoding="utf-8") as file:
    for row in rows:
        file.write(json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n")

print(len(rows))
