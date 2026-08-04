(ns webapp.connections.views.resource-catalog.helpers-test
  "Feature-flag gating for the resource catalog.

  The gate decides whether a resource type can be created at all, so both
  directions matter: a flag left off must not leak the card, and a flag turned
  on must not hide anything else."
  (:require
   [cljs.test :refer-macros [deftest testing is]]
   [re-frame.core :as rf]
   [re-frame.db :as rf-db]
   [webapp.connections.views.resource-catalog.helpers :as helpers]
   ;; Registers :feature-flag/enabled?, which the gate subscribes to. Required
   ;; explicitly: without it the subscription is unregistered and every
   ;; assertion fails on a nil deref rather than on the behaviour under test.
   [webapp.subs]))

(def ^:private metadata
  [{:id "postgres" :name "PostgreSQL"}
   {:id "mcpproxy" :name "MCP Gateway"}
   {:id "ssh" :name "SSH"}])

(defn- with-flags!
  "Install a feature-flag snapshot shaped like /serverinfo's response.
  Subscriptions memoise per app-db value, so the cache is cleared between
  cases or the second assertion reads the first one's answer."
  [flags]
  (reset! rf-db/app-db {:gateway->info {:data {:feature_flags flags}}})
  (rf/clear-subscription-cache!))

(defn- catalog-ids []
  (set (map :id (helpers/compose-connections metadata false))))

;; An org that never heard of the flag must not be offered the type. This is
;; the default state, so getting it wrong ships the feature to everyone.
(deftest flagged-type-hidden-when-flag-is-absent
  (with-flags! nil)
  (is (true? (helpers/flag-hidden? {:id "mcpproxy"})))
  (is (not (contains? (catalog-ids) "mcpproxy"))))

(deftest flagged-type-hidden-when-flag-is-false
  (with-flags! {:experimental.mcp_gateway false})
  (is (true? (helpers/flag-hidden? {:id "mcpproxy"})))
  (is (not (contains? (catalog-ids) "mcpproxy"))))

(deftest flagged-type-visible-when-flag-is-enabled
  (with-flags! {:experimental.mcp_gateway true})
  (is (false? (helpers/flag-hidden? {:id "mcpproxy"})))
  (is (contains? (catalog-ids) "mcpproxy")))

;; /serverinfo serialises flag names as JSON strings. Whether they arrive
;; keywordized depends on the fetch path, so both shapes must resolve or the
;; card silently disappears for an org that enabled it.
(deftest flag-lookup-accepts-string-keys
  (with-flags! {"experimental.mcp_gateway" true})
  (is (false? (helpers/flag-hidden? {:id "mcpproxy"})))
  (is (contains? (catalog-ids) "mcpproxy")))

;; The gate must be surgical: it removes one card, not the catalog.
(deftest unflagged-types-are-never-gated
  (with-flags! {})
  (is (false? (helpers/flag-hidden? {:id "postgres"})))
  (is (false? (helpers/flag-hidden? {:id "some-future-type"})))
  (let [ids (catalog-ids)]
    (is (contains? ids "postgres"))
    (is (contains? ids "ssh"))
    ;; custom entries are appended after filtering and must survive it
    (is (contains? ids "linux-vm"))))
