(ns webapp.provisioning.views.session-list
  (:require
   ["@radix-ui/themes" :refer [Badge Box Flex Heading Text]]
   ["lucide-react" :refer [Check ChevronDown ChevronUp Info X]]
   ["react" :as react]
   [re-frame.core :as rf]
   [webapp.provisioning.data :as data]
   [webapp.provisioning.views.shared :as shared]))

(def ^:private session-status
  {"success" {:color "green" :icon [:> Check {:size 10}]}
   "error"   {:color "red"   :icon [:> X {:size 10}]}})

(defn- session-status-badge [{:keys [status]} & [opts]]
  (let [{:keys [color icon]} (get session-status status
                                  (get session-status "error"))]
    [:> Badge {:color color :variant "soft" :size "1"}
     icon (str " " (if (:plain? opts) status status))]))

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

(defn- terminal-body
  "Renders the dark terminal body. Height tracks the content — no max-height
   and no vertical scrollbar inside the terminal, so a 6-line session takes
   up exactly 6 lines of dark space (the page already scrolls if needed via
   the session-list's outer overflow container). Horizontal overflow stays
   auto so a stray long line doesn't blow out the row width.

   When `loaded?` is false we show a faint 'Loading…' line instead of the
   empty `output` placeholder — sessions synthesized from plan-items start
   empty and only fill in once the user expands the row."
  [{:keys [output loaded?]}]
  [:> Box {:style {:background "var(--gray-12)"
                   :padding "14px 18px"
                   :overflow-x "auto"}}
   [:> Text {:size "1"
             :style {:color "var(--gray-4)"
                     :font-family "var(--font-mono)"
                     :font-size 11.5
                     :white-space "pre"
                     :display "block"
                     :line-height 1.72}}
    (if (and (not loaded?) (empty? output))
      "Loading session output\u2026"
      output)]])

(defn- session-row
  [{:keys [session index total expanded? on-toggle]}]
  (let [{:keys [resource-name resource-type role-name status duration-ms output loaded?]} session
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
         [terminal-body {:output output :loaded? loaded?}]]])]))

(defn- session-list-screen-inner
  [{:keys [sessions title subtitle on-back]}]
  (let [[expanded-id set-expanded-id] (react/useState nil)
        toggle-expanded!
        (fn [id]
          (let [collapsing? (= expanded-id id)
                next-id     (when-not collapsing? id)]
            ;; Lazy-load: when expanding a synthesized (not-yet-loaded)
            ;; row, kick off the fetch so the output streams in. Already
            ;; loaded rows and collapse actions are no-ops here.
            (when next-id
              (let [sess (some #(when (= (:id %) next-id) %) sessions)]
                (when (and sess (not (:loaded? sess)))
                  (rf/dispatch [:provisioning/fetch-plan-session next-id]))))
            (set-expanded-id next-id)))]
    [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
     [:> Flex {:align "center" :gap "2" :mb "1"}
      [shared/back-button {:on-click on-back}]]

     [:> Flex {:align "center" :justify "between" :mb "4"}
      [:> Flex {:direction "column" :gap "1"}
       [:> Heading {:size "7"} title]
       (when subtitle
         [:> Text {:size "2" :color "gray"} subtitle])]
      [:> Text {:size "1" :color "gray"}
       (data/pluralize (count sessions) "session")]]

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
         [session-row {:session   s
                       :index     i
                       :total     (count sessions)
                       :expanded? (= expanded-id (:id s))
                       :on-toggle #(toggle-expanded! (:id s))}]))]]))

(defn session-list-screen [props]
  [:f> session-list-screen-inner props])
