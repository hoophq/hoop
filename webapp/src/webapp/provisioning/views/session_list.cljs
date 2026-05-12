(ns webapp.provisioning.views.session-list
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Heading Skeleton Text]]
   ["lucide-react" :refer [AlertCircle Brain Check ChevronDown ChevronUp
                            Info X Zap]]
   [clojure.string :as cs]
   [reagent.core :as r]
   [webapp.provisioning.data :as data]
   [webapp.provisioning.views.shared :as shared]))

(def ^:private severity
  {"warning"        {:color "amber"  :icon [:> AlertCircle {:size 14}]}
   "recommendation" {:color "indigo" :icon [:> Zap {:size 14}]}
   "info"           {:color "gray"   :icon [:> Info {:size 14}]}})

(defn- severity-for [s] (get severity s (get severity "info")))

(def ^:private session-status
  {"success" {:color "green" :icon [:> Check {:size 10}]}
   "error"   {:color "red"   :icon [:> X {:size 10}]}})

(defn- session-status-badge [{:keys [status]} & [opts]]
  (let [{:keys [color icon]} (get session-status status 
                                  (get session-status "error"))]
    [:> Badge {:color color :variant "soft" :size "1"}
     icon (str " " (if (:plain? opts) status status))]))

;; ── AI insight generation ────────────────────────────────────────────────
(defn- generate-insights
  "Builds the list of AI insights for a set of sessions. Each insight is
   conditional except the trailing guardrails tip, which always appears."
  [sessions]
  (let [failed     (filter #(= "error" (:status %)) sessions)
        unique     (set (map :resource-name sessions))
        has-admin? (some #(cs/includes? (:output %) "CREATE USER") sessions)]
    (cond-> []
      (pos? (count failed))
      (conj {:id "failed" :severity "warning"
             :title  (str (data/pluralize (count failed) "session")
                          " failed during provisioning")
             :detail (str "Resources unreachable: "
                          (cs/join ", " (distinct (map :resource-name failed)))
                          ". Check network connectivity and verify agent is reachable.")})

      (pos? (count unique))
      (conj {:id "masking" :severity "recommendation"
             :title  "Data masking recommended \u2014 PII exposure risk detected"
             :detail (str (data/pluralize (count unique) "provisioned resource")
                          " have SELECT-enabled roles without data masking.")})

      has-admin?
      (conj {:id "jit" :severity "recommendation"
             :title  "Admin accounts should use JIT access"
             :detail "Admin accounts with permanent access increase blast radius of credential compromise. JIT reduces exposure to minutes."})

      :always
      (conj {:id "guardrails" :severity "info"
             :title  "Guardrails can prevent accidental data loss"
             :detail "Blocking DELETE, DROP, and TRUNCATE at the Hoop layer requires no schema changes and catches mistakes before they reach the database engine."}))))

;; ── Sub-components ────────────────────────────────────────────────────────

(defn- ai-loading-skeleton []
  [:> Flex {:direction "column" :gap "2" :px "4" :py "4"}
   (for [i (range 3)]
     ^{:key i}
     [:> Flex {:gap "3" :align "start"}
      [:> Skeleton {:width "16px" :height "16px"
                    :style {:border-radius "var(--radius-2)" :flex-shrink 0}}]
      [:> Flex {:direction "column" :gap "1" :style {:flex 1}}
       [:> Skeleton {:width "60%" :height "14px"}]
       [:> Skeleton {:width "90%" :height "12px"}]]])])

(defn- insight-row [ins]
  (let [{:keys [color icon]} (severity-for (:severity ins))]
    [:> Flex {:gap "3" :align "start"}
     [:> Box {:style {:color (str "var(--" color "-9)")
                      :display "flex" :flex-shrink 0 :margin-top 2}}
      icon]
     [:> Flex {:direction "column" :gap "0"}
      [:> Text {:size "2" :weight "medium"} (:title ins)]
      [:> Text {:size "1" :color "gray"} (:detail ins)]]]))

(defn- ai-panel [{:keys [open? loading? insights on-close]}]
  (when open?
    [:> Box {:mb "4"
             :style {:border "1px solid var(--indigo-5)"
                     :border-radius "var(--radius-3)"
                     :background "var(--indigo-1)"
                     :overflow "hidden"}}
     [:> Flex {:align "center" :justify "between" :px "4" :py "3"
               :style {:border-bottom "1px solid var(--indigo-4)"}}
      [:> Flex {:align "center" :gap "2"}
       [:> Box {:style {:color "var(--indigo-9)" :display "flex"}}
        [:> Brain {:size 15}]]
       [:> Text {:size "2" :weight "medium"} "AI Session Analysis"]
       (when loading?
         [:> Badge {:color "indigo" :variant "soft" :size "1"}
          [shared/spinner {:color "indigo" :size 9}] " Analyzing…"])
       (when (and (not loading?) (pos? (count insights)))
         [:> Badge {:color "indigo" :variant "soft" :size "1"}
          (data/pluralize (count insights) "insight")])]
      [:> Button {:size "1" :variant "ghost" :color "gray"
                  :on-click on-close}
       [:> X {:size 12}]]]
     (if loading?
       [ai-loading-skeleton]
       [:> Flex {:direction "column" :px "4" :py "3" :gap "3"}
        (for [ins insights]
          ^{:key (:id ins)}
          [insight-row ins])])]))

(defn- format-duration [ms]
  (if (>= ms 1000)
    (str (.toFixed (/ ms 1000) 1) "s")
    (str ms "ms")))

(defn- terminal-chrome [{:keys [title status]}]
  [:> Flex {:align "center" :gap "2" :px "3"
            :style {:height 32
                    :background "var(--gray-11)"
                    :border-bottom "1px solid var(--gray-10)"}}
   (for [c ["red" "yellow" "green"]]
     ^{:key c}
     [:> Box {:style {:width 9 :height 9 :border-radius "50%"
                      :background (str "var(--" c "-8)") :flex-shrink 0}}])
   [:> Text {:size "1" :style {:flex 1 :text-align "center"
                                :font-family "var(--font-mono)"
                                :font-size 10 :color "var(--gray-8)"}}
    title]
   [session-status-badge {:status status} {:plain? true}]])

(defn- terminal-body [output]
  [:> Box {:style {:background "var(--gray-12)"
                   :padding "14px 18px"
                   :overflow "auto" :max-height 260}}
   [:> Text {:size "1"
             :style {:color "var(--gray-4)"
                     :font-family "var(--font-mono)"
                     :font-size 11.5
                     :white-space "pre"
                     :display "block"
                     :line-height 1.72}}
    output]])

(defn- session-row
  [{:keys [session index total expanded? on-toggle]}]
  (let [{:keys [resource-name resource-type role-name status duration-ms output]} session
        last? (= index (dec total))
        bg    (shared/zebra-bg index)]
    [:<>
     [:> Flex {:px "3" :py "2" :align "center"
               :on-click on-toggle
               :style {:border-bottom (if expanded? "none"
                                        (when-not last? "1px solid var(--gray-3)"))
                       :min-height 44
                       :background (if expanded? "var(--indigo-1)" bg)
                       :cursor "pointer"}}
      [:> Flex {:direction "column" :gap "0" :style {:flex "3 1 0"}}
       [:> Flex {:align "center" :gap "2"}
        [:> Text {:size "2" :weight "medium"} resource-name]
        [:> Badge {:color "gray" :variant "soft" :size "1"} resource-type]]
       [:> Text {:size "1" :color "gray"
                 :style {:font-family "var(--font-mono)" :font-size 11}}
        role-name]]
      [:> Box {:style {:width 90 :flex-shrink 0}}
       [session-status-badge session]]
      [:> Box {:style {:width 80 :flex-shrink 0 :text-align "right"}}
       [:> Text {:size "1" :color "gray"} (format-duration duration-ms)]]
      [:> Box {:style {:width 28 :display "flex" :justify-content "center"
                       :color "var(--gray-9)"}}
       (if expanded? [:> ChevronUp {:size 14}] [:> ChevronDown {:size 14}])]]
     (when expanded?
       [:> Box {:px "3" :pb "3"
                :style {:background bg
                        :border-bottom (when-not last? "1px solid var(--gray-3)")}}
        [:> Box {:style {:border-radius "var(--radius-2)"
                         :overflow "hidden"
                         :border "1px solid var(--gray-a4)"}}
         [terminal-chrome {:title (str resource-name " — " role-name)
                           :status status}]
         [terminal-body output]]])]))

;; ── Main screen ──────────────────────────────────────────────────────────
(defn session-list-screen
  [_props]
  (let [expanded-id (r/atom nil)
        ai-open     (r/atom false)
        ai-loading  (r/atom false)
        ai-insights (r/atom [])
        on-ai-open  (fn [sessions]
                      (reset! ai-open true)
                      (when (empty? @ai-insights)
                        (reset! ai-loading true)
                        (js/setTimeout
                         (fn []
                           (reset! ai-insights (generate-insights sessions))
                           (reset! ai-loading false))
                         1800)))]
    (fn [{:keys [sessions title subtitle on-back]}]
      [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
       [:> Flex {:align "center" :gap "2" :mb "1"}
        [shared/back-button {:on-click on-back}]]

       [:> Flex {:align "center" :justify "between" :mb "4"}
        [:> Flex {:direction "column" :gap "1"}
         [:> Heading {:size "7"} title]
         (when subtitle
           [:> Text {:size "2" :color "gray"} subtitle])]
        [:> Flex {:align "center" :gap "3"}
         [:> Text {:size "1" :color "gray"}
          (data/pluralize (count sessions) "session")]
         [:> Button {:size "1"
                     :variant (if @ai-open "solid" "outline")
                     :color   (if @ai-open "indigo" "gray")
                     :on-click #(on-ai-open sessions)}
          [:> Brain {:size 13}] " AI Analysis"]]]

       [ai-panel {:open?    @ai-open
                  :loading? @ai-loading
                  :insights @ai-insights
                  :on-close #(reset! ai-open false)}]

       [shared/info-callout
        {:color "indigo" :mb "4" :size "1"
         :icon  [:> Info {:size 14}]
         :text  (str "Each provisioning step automatically creates an administrative session. "
                     "Sessions capture the full SQL output for auditing.")}]

       [:> Box {:style {:flex 1 :overflow-y "auto"
                        :border "1px solid var(--gray-5)"
                        :border-radius "var(--radius-2)"}}
        [shared/flex-table-header
         [{:flex "3 1 0" :label "Resource · Role"}
          {:width 90      :label "Status"}
          {:width 80      :label "Duration"}
          {:width 28      :label ""}]]

        (when (empty? sessions)
          [:> Flex {:align "center" :justify "center" :py "8"}
           [:> Text {:size "2" :color "gray"} "No sessions for this run yet."]])

        (doall
         (for [[i s] (map-indexed vector sessions)]
           ^{:key (:id s)}
           [session-row {:session    s
                         :index      i
                         :total      (count sessions)
                         :expanded?  (= @expanded-id (:id s))
                         :on-toggle  #(reset! expanded-id (if (= @expanded-id (:id s))
                                                            nil (:id s)))}]))]])))
