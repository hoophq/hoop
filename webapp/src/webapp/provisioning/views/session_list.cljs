(ns webapp.provisioning.views.session-list
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Callout Flex Heading
                                Skeleton Text]]
   ["lucide-react" :refer [AlertCircle ArrowLeft Brain Check ChevronDown
                            ChevronUp Info Loader2 X Zap]]
   [clojure.string :as cs]
   [reagent.core :as r]))

;; ── AI insight generation ──────────────────────────────────────────────────────
(defn- generate-insights [sessions]
  (let [failed  (filter #(= "error" (:status %)) sessions)
        unique  (set (map :resource-name sessions))
        has-admin? (some #(cs/includes? (:output %) "CREATE USER") sessions)
        insights (atom [])]
    (when (pos? (count failed))
      (swap! insights conj
             {:id "failed" :severity "warning"
              :title (str (count failed) " session"
                          (when (not= 1 (count failed)) "s")
                          " failed during provisioning")
              :detail (str "Resources unreachable: "
                           (cs/join ", "
                                                (distinct (map :resource-name failed)))
                           ". Check network connectivity and verify agent is reachable.")}))
    (when (pos? (count unique))
      (swap! insights conj
             {:id "masking" :severity "recommendation"
              :title "Data masking recommended — PII exposure risk detected"
              :detail (str (count unique) " provisioned resource"
                           (when (not= 1 (count unique)) "s")
                           " have SELECT-enabled roles without data masking.")}))
    (when has-admin?
      (swap! insights conj
             {:id "jit" :severity "recommendation"
              :title "Admin accounts should use JIT access"
              :detail "Admin accounts with permanent access increase blast radius of credential compromise. JIT reduces exposure to minutes."}))
    (swap! insights conj
           {:id "guardrails" :severity "info"
            :title "Guardrails can prevent accidental data loss"
            :detail "Blocking DELETE, DROP, and TRUNCATE at the Hoop layer requires no schema changes and catches mistakes before they reach the database engine."})
    @insights))

(defn- severity-color [s]
  (case s "warning" "amber" "recommendation" "indigo" "gray"))

(defn- severity-icon [s]
  (case s
    "warning"        [:> AlertCircle {:size 14}]
    "recommendation" [:> Zap {:size 14}]
    [:> Info {:size 14}]))

;; ── Session list screen ────────────────────────────────────────────────────────
(defn session-list-screen
  [_props]
  (let [expanded-id (r/atom nil)
        ai-open     (r/atom false)
        ai-loading  (r/atom false)
        ai-insights (r/atom [])]
    (fn [{:keys [sessions title subtitle on-back]}]
      [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
       ;; Back
       [:> Flex {:align "center" :gap "2" :mb "1"}
        [:> Button {:variant "ghost" :color "gray" :size "2" :on-click on-back}
         [:> ArrowLeft {:size 14}] " Back"]]

       ;; Header
       [:> Flex {:align "center" :justify "between" :mb "4"}
        [:> Flex {:direction "column" :gap "1"}
         [:> Heading {:size "7"} title]
         (when subtitle
           [:> Text {:size "2" :color "gray"} subtitle])]
        [:> Flex {:align "center" :gap "3"}
         [:> Text {:size "1" :color "gray"}
          (str (count sessions) " session" (when (not= 1 (count sessions)) "s"))]
         [:> Button {:size "1"
                     :variant (if @ai-open "solid" "outline")
                     :color   (if @ai-open "indigo" "gray")
                     :on-click (fn []
                                 (reset! ai-open true)
                                 (when (empty? @ai-insights)
                                   (reset! ai-loading true)
                                   (js/setTimeout
                                    (fn []
                                      (reset! ai-insights (generate-insights sessions))
                                      (reset! ai-loading false))
                                    1800)))}
          [:> Brain {:size 13}] " AI Analysis"]]]

       ;; AI analysis panel
       (when @ai-open
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
            (when @ai-loading
              [:> Badge {:color "indigo" :variant "soft" :size "1"}
               [:span {:class "animate-spin inline-flex" :style {:margin-right 3}}
                [:> Loader2 {:size 9}]]
               "Analyzing…"])
            (when (and (not @ai-loading) (pos? (count @ai-insights)))
              [:> Badge {:color "indigo" :variant "soft" :size "1"}
               (str (count @ai-insights) " insight"
                    (when (not= 1 (count @ai-insights)) "s"))])]
           [:> Button {:size "1" :variant "ghost" :color "gray"
                       :on-click #(reset! ai-open false)}
            [:> X {:size 12}]]]

          ;; Loading skeleton
          (when @ai-loading
            [:> Flex {:direction "column" :gap "2" :px "4" :py "4"}
             (for [i (range 3)]
               ^{:key i}
               [:> Flex {:gap "3" :align "start"}
                [:> Skeleton {:width "16px" :height "16px"
                              :style {:border-radius "var(--radius-2)" :flex-shrink 0}}]
                [:> Flex {:direction "column" :gap "1" :style {:flex 1}}
                 [:> Skeleton {:width "60%" :height "14px"}]
                 [:> Skeleton {:width "90%" :height "12px"}]]])])

          ;; Insights
          (when-not @ai-loading
            [:> Flex {:direction "column" :px "4" :py "3" :gap "3"}
             (for [ins @ai-insights]
               ^{:key (:id ins)}
               [:> Flex {:gap "3" :align "start"}
                [:> Box {:style {:color (str "var(--" (severity-color (:severity ins)) "-9)")
                                 :display "flex" :flex-shrink 0 :margin-top 2}}
                 (severity-icon (:severity ins))]
                [:> Flex {:direction "column" :gap "0"}
                 [:> Text {:size "2" :weight "medium"} (:title ins)]
                 [:> Text {:size "1" :color "gray"} (:detail ins)]]])])])

       ;; Info callout
       [:> Callout.Root {:color "indigo" :mb "4" :size "1"}
        [:> Callout.Icon [:> Info {:size 14}]]
        [:> Callout.Text {:size "1"}
         "Each provisioning step automatically creates an administrative session. "
         "Sessions capture the full SQL output for auditing."]]

       ;; Session rows
       [:> Box {:style {:flex 1 :overflow-y "auto"
                        :border "1px solid var(--gray-5)"
                        :border-radius "var(--radius-2)"}}
        ;; Header
        [:> Flex {:px "3" :py "2"
                  :style {:background "var(--gray-3)"
                          :border-bottom "1px solid var(--gray-5)"
                          :position "sticky" :top 0}}
         [:> Box {:style {:flex "3 1 0"}}
          [:> Text {:size "1" :color "gray" :weight "medium"} "Resource · Role"]]
         [:> Box {:style {:width 90 :flex-shrink 0}}
          [:> Text {:size "1" :color "gray" :weight "medium"} "Status"]]
         [:> Box {:style {:width 80 :flex-shrink 0 :text-align "right"}}
          [:> Text {:size "1" :color "gray" :weight "medium"} "Duration"]]
         [:> Box {:style {:width 28}}]]

        (when (empty? sessions)
          [:> Flex {:align "center" :justify "center" :py "8"}
           [:> Text {:size "2" :color "gray"} "No sessions for this run yet."]])

        (doall
         (for [[i s] (map-indexed vector sessions)]
           (let [expanded? (= @expanded-id (:id s))]
             ^{:key (:id s)}
             [:<>
              ;; Row
              [:> Flex {:px "3" :py "2" :align "center"
                        :on-click #(reset! expanded-id (if expanded? nil (:id s)))
                        :style {:border-bottom (if expanded? "none"
                                                 (when (< i (dec (count sessions)))
                                                   "1px solid var(--gray-3)"))
                                :min-height 44
                                :background (if expanded? "var(--indigo-1)"
                                              (if (even? i) "var(--color-panel-solid)" "var(--gray-1)"))
                                :cursor "pointer"}}
               [:> Flex {:direction "column" :gap "0" :style {:flex "3 1 0"}}
                [:> Flex {:align "center" :gap "2"}
                 [:> Text {:size "2" :weight "medium"} (:resource-name s)]
                 [:> Badge {:color "gray" :variant "soft" :size "1"} (:resource-type s)]]
                [:> Text {:size "1" :color "gray"
                          :style {:font-family "var(--font-mono)" :font-size 11}}
                 (:role-name s)]]
               [:> Box {:style {:width 90 :flex-shrink 0}}
                [:> Badge {:color (if (= "success" (:status s)) "green" "red")
                           :variant "soft" :size "1"}
                 (if (= "success" (:status s))
                   [:> Check {:size 10}]
                   [:> X {:size 10}])
                 (str " " (:status s))]]
               [:> Box {:style {:width 80 :flex-shrink 0 :text-align "right"}}
                [:> Text {:size "1" :color "gray"}
                 (if (>= (:duration-ms s) 1000)
                   (str (.toFixed (/ (:duration-ms s) 1000) 1) "s")
                   (str (:duration-ms s) "ms"))]]
               [:> Box {:style {:width 28 :display "flex" :justify-content "center"
                                :color "var(--gray-9)"}}
                (if expanded?
                  [:> ChevronUp {:size 14}]
                  [:> ChevronDown {:size 14}])]]

              ;; Expanded terminal output
              (when expanded?
                [:> Box {:px "3" :pb "3"
                         :style {:background (if (even? i) "var(--color-panel-solid)" "var(--gray-1)")
                                 :border-bottom (when (< i (dec (count sessions)))
                                                  "1px solid var(--gray-3)")}}
                 [:> Box {:style {:border-radius "var(--radius-2)"
                                  :overflow "hidden"
                                  :border "1px solid var(--gray-a4)"}}
                  ;; Terminal chrome bar
                  [:> Flex {:align "center" :gap "2" :px "3"
                            :style {:height 32
                                    :background "var(--gray-11)"
                                    :border-bottom "1px solid var(--gray-10)"}}
                   [:> Box {:style {:width 9 :height 9 :border-radius "50%"
                                    :background "var(--red-8)" :flex-shrink 0}}]
                   [:> Box {:style {:width 9 :height 9 :border-radius "50%"
                                    :background "var(--yellow-8)" :flex-shrink 0}}]
                   [:> Box {:style {:width 9 :height 9 :border-radius "50%"
                                    :background "var(--green-8)" :flex-shrink 0}}]
                   [:> Text {:size "1" :style {:flex 1 :text-align "center"
                                               :font-family "var(--font-mono)"
                                               :font-size 10 :color "var(--gray-8)"}}
                    (str (:resource-name s) " — " (:role-name s))]
                   [:> Badge {:color (if (= "success" (:status s)) "green" "red")
                              :variant "soft" :size "1"}
                    (:status s)]]
                  ;; Terminal body
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
                    (:output s)]]]])])))]])))
