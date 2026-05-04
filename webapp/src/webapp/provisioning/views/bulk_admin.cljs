(ns webapp.provisioning.views.bulk-admin
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Card Flex Heading Select
                                Text TextField]]
   ["lucide-react" :refer [ArrowLeft Check FileText Loader2 Server
                            Upload UserCog]]
   [reagent.core :as r]
   [webapp.provisioning.data :as data]))

(defn bulk-admin-screen
  "Manual + CSV admin credential entry. No vault support."
  [props]
  (let [mode         (r/atom (or (:initial-mode props) "manual"))
        csv-parsing  (r/atom false)
        csv-parsed   (r/atom false)
        agent-id     (r/atom "default")]
    (fn [{:keys [resources configs set-configs on-apply on-cancel]}]
      (let [cfg configs]
        [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
         ;; Back button
         [:> Flex {:align "center" :gap "2" :mb "1"}
          [:> Button {:variant "ghost" :color "gray" :size "2" :on-click on-cancel}
           [:> ArrowLeft {:size 14}] " Back"]]

         [:> Flex {:align "baseline" :gap "3" :mb "4"}
          [:> Heading {:size "7"} "Manage — admin accounts"]
          [:> Badge {:color "gray" :variant "soft"} (str (count resources) " resources")]]

         ;; Agent selector
         [:> Card {:mb "4" :style {:background "var(--gray-2)" :border-color "var(--gray-4)"}}
          [:> Flex {:align "center" :gap "3"}
           [:> Box {:style {:width 36 :height 36 :border-radius "var(--radius-2)" :flex-shrink 0
                            :background "var(--indigo-3)" :color "var(--indigo-9)"
                            :display "flex" :align-items "center" :justify-content "center"}}
            [:> Server {:size 17}]]
           [:> Flex {:direction "column" :gap "0" :style {:flex 1}}
            [:> Text {:size "2" :weight "medium"} "Agent"]
            [:> Flex {:align "center" :gap "1"}
             [:> Box {:class "animate-pulse"
                      :style {:width 6 :height 6 :border-radius "50%"
                              :background "var(--green-9)" :flex-shrink 0}}]
             [:> Text {:size "1" :color "gray"} "Handles connectivity to all selected resources"]]]
           [:> Select.Root {:size "1" :value @agent-id
                            :onValueChange #(reset! agent-id %)}
            [:> Select.Trigger {:style {:width 240}}]
            [:> Select.Content
             (for [a data/mock-agents]
               ^{:key (:id a)}
               [:> Select.Item {:value (:id a)}
                (str (:name a) " — " (:env a))])]]]]

         ;; Mode toggle
         [:> Flex {:gap "2" :mb "4"}
          [:> Button {:size "2"
                      :variant (if (= @mode "manual") "solid" "outline")
                      :color   (if (= @mode "manual") "indigo" "gray")
                      :on-click #(reset! mode "manual")}
           [:> UserCog {:size 14}] " Enter manually"]
          [:> Button {:size "2"
                      :variant (if (= @mode "csv") "solid" "outline")
                      :color   (if (= @mode "csv") "indigo" "gray")
                      :on-click #(reset! mode "csv")}
           [:> Upload {:size 14}] " Import from CSV"]]

         ;; Manual mode
         (when (= @mode "manual")
           [:<>
            ;; Table header
            [:> Flex {:px "3" :py "2"
                      :style {:background "var(--gray-3)"
                              :border-radius "var(--radius-2) var(--radius-2) 0 0"
                              :border-bottom "1px solid var(--gray-5)"
                              :flex-shrink 0}}
             [:> Box {:style {:width 260 :flex-shrink 0}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Resource"]]
             [:> Box {:style {:width 150 :flex-shrink 0}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Admin user"]]
             [:> Box {:style {:flex 1}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Password"]]]
            ;; Rows
            [:> Box {:style {:flex 1 :overflow-y "auto"
                             :border "1px solid var(--gray-5)" :border-top "none"
                             :border-radius "0 0 var(--radius-2) var(--radius-2)"}}
             (doall
              (for [[i r] (map-indexed vector resources)]
                (let [c (get cfg (:id r))]
                  ^{:key (:id r)}
                  [:> Flex {:align "center" :px "3" :py "2"
                            :style {:border-bottom (when (< i (dec (count resources)))
                                                     "1px solid var(--gray-3)")
                                    :min-height 52
                                    :background (if (even? i) "var(--color-panel-solid)" "var(--gray-1)")}}
                   [:> Box {:style {:width 260 :flex-shrink 0}}
                    [:> Flex {:align "center" :gap "2"}
                     [:> Text {:size "2" :weight "medium"} (:name r)]
                     [:> Badge {:color "gray" :variant "soft" :size "1"} (:db-type r)]]
                    [:> Text {:size "1" :color "gray"
                              :style {:font-family "var(--font-mono)" :font-size 11}}
                     (:host r)]]
                   [:> Box {:style {:width 150 :flex-shrink 0}}
                    [:> TextField.Root {:size "1" :placeholder "Admin user"
                                        :value (or (:username c) "")
                                        :onChange #(set-configs
                                                  (fn [c] (assoc-in c [(:id r) :username]
                                                                    (.. % -target -value))))}]]
                   [:> Box {:style {:flex 1}}
                    [:> TextField.Root {:size "1" :type "password" :placeholder "Password"
                                        :value (or (:password c) "")
                                        :onChange #(set-configs
                                                  (fn [c] (assoc-in c [(:id r) :password]
                                                                    (.. % -target -value))))}]]])))]])

         ;; CSV mode — upload
         (when (and (= @mode "csv") (not @csv-parsed))
           [:> Flex {:direction "column" :gap "3" :style {:flex 1}}
            [:> Box {:on-click (fn []
                                 (reset! csv-parsing true)
                                 (js/setTimeout (fn []
                                                  (reset! csv-parsing false)
                                                  (reset! csv-parsed true))
                                                900))
                     :style {:border "2px dashed var(--gray-6)"
                             :border-radius "var(--radius-3)"
                             :padding 40 :background "var(--gray-2)"
                             :text-align "center" :cursor "pointer"
                             :flex 1 :display "flex" :align-items "center"
                             :justify-content "center"}}
             (if @csv-parsing
               [:> Flex {:direction "column" :align "center" :gap "2"}
                [:span {:class "animate-spin inline-flex" :style {:color "var(--indigo-9)"}}
                 [:> Loader2 {:size 20}]]
                [:> Text {:size "2" :color "gray"} "Parsing CSV…"]]
               [:> Flex {:direction "column" :align "center" :gap "2"}
                [:> Upload {:size 24 :stroke-width 1.5 :color "var(--gray-9)"}]
                [:> Text {:size "2" :color "gray"}
                 "Drop your CSV here or "
                 [:> Text {:size "2" :color "indigo" :style {:cursor "pointer"}} "browse"]]
                [:> Text {:size "1" :color "gray"}
                 "Columns: resource_name, admin_user, password"]])]
            [:> Flex {:justify "end"}
             [:> Button {:variant "ghost" :size "1" :color "gray"}
              [:> FileText {:size 12}] " Download template"]]])

         ;; CSV mode — parsed preview
         (when (and (= @mode "csv") @csv-parsed)
           [:> Box {:style {:flex 1 :overflow-y "auto"
                            :border "1px solid var(--gray-5)"
                            :border-radius "var(--radius-2)"}}
            [:> Flex {:px "3" :py "2"
                      :style {:background "var(--gray-3)"
                              :border-bottom "1px solid var(--gray-5)"}}
             [:> Box {:style {:flex 1}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Resource"]]
             [:> Box {:style {:width 120 :flex-shrink 0}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Admin user"]]
             [:> Box {:style {:width 220 :flex-shrink 0}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Credential"]]
             [:> Box {:style {:width 80}}
              [:> Text {:size "1" :color "gray" :weight "medium"} "Status"]]]
            (doall
             (for [[i r] (map-indexed vector resources)]
               ^{:key (:id r)}
               [:> Flex {:px "3" :py "2" :align "center"
                         :style {:border-bottom (when (< i (dec (count resources)))
                                                  "1px solid var(--gray-3)")
                                 :min-height 44
                                 :background (if (even? i) "var(--color-panel-solid)" "var(--gray-1)")}}
                [:> Flex {:align "center" :gap "2" :style {:flex 1}}
                 [:> Text {:size "2" :weight "medium"} (:name r)]
                 [:> Badge {:color "gray" :variant "soft" :size "1"} (:db-type r)]]
                [:> Box {:style {:width 120 :flex-shrink 0}}
                 [:> Text {:size "2"} "admin"]]
                [:> Box {:style {:width 220 :flex-shrink 0}}
                 [:> Text {:size "2" :color "gray"
                           :style {:font-family "var(--font-mono)" :font-size 11}}
                  (str "csv://credentials/" (:name r))]]
                [:> Badge {:color "green" :variant "soft" :size "1"}
                 [:> Check {:size 10}] " Valid"]]))])

         ;; Footer
         [:> Flex {:align "center" :justify "end" :gap "3" :pt "4" :mt "4"
                   :style {:border-top "1px solid var(--gray-4)" :flex-shrink 0}}
          [:> Button {:variant "outline" :color "gray" :on-click on-cancel} "Cancel"]
          [:> Button {:disabled (and (= @mode "csv") (not @csv-parsed))
                      :on-click #(on-apply cfg @agent-id)}
           (str "Apply to " (count resources)
                (if (= 1 (count resources)) " resource" " resources")
                " →")]]]))))
