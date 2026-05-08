(ns webapp.provisioning.views.bulk-admin
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Card Flex Heading
                                IconButton Progress Select Text TextField]]
   ["lucide-react" :refer [AlertCircle Check CheckCircle2 Eye EyeOff
                            Server Upload UserCog X]]
   ["react" :as react]
   [re-frame.core :as rf]
   [webapp.provisioning.data :as data]
   [webapp.provisioning.views.shared :as shared]))

(defn- rows->admin-credentials
  "Folds parsed CSV rows into {resource-name {:username ... :password ...}}."
  [rows]
  (reduce
   (fn [acc row]
     (if (seq (:name row))
       (assoc acc (:name row)
              {:username (or (:admin_user row) "")
               :password (or (:password row) "")})
       acc))
   {}
   rows))

(defn- mask-password
  "Hides the middle of a password with `*`. Keeps the first 2 and last 2
   characters visible for context. Passwords <= 4 chars are fully masked.
   The base64 encoding still happens server-side; this is display-only."
  [pw]
  (let [n (count pw)]
    (cond
      (zero? n) ""
      (<= n 4)  (apply str (repeat n "*"))
      :else     (str (subs pw 0 2)
                     (apply str (repeat (- n 4) "*"))
                     (subs pw (- n 2))))))

(defn- eye-toggle
  "Small ghost icon button that flips between Eye / EyeOff."
  [{:keys [visible? on-click]}]
  [:> IconButton {:size "1" :variant "ghost" :color "gray"
                  :type "button"
                  :aria-label (if visible? "Hide password" "Show password")
                  :on-click on-click}
   (if visible? [:> EyeOff {:size 12}] [:> Eye {:size 12}])])

(defn- bulk-admin-screen-inner
  [{:keys [resources configs set-configs initial-mode on-cancel on-done]}]
  (let [[mode set-mode]                       (react/useState (or initial-mode "manual"))
        [csv-parsed set-csv-parsed]           (react/useState false)
        [csv-match-count set-csv-match-count] (react/useState 0)
        [csv-row-count set-csv-row-count]     (react/useState 0)
        [agent-id set-agent-id]               (react/useState "")
        [applying? set-applying]              (react/useState false)
        [apply-progress set-apply-progress]   (react/useState 0)
        [apply-results set-apply-results]     (react/useState nil)
        [visible-pwds set-visible-pwds]       (react/useState #{})
        toggle-pwd!  (fn [id]
                       (set-visible-pwds
                        (fn [s] (if (contains? s id) (disj s id) (conj s id)))))
        agents                                @(rf/subscribe [:agents])
        agents-data                           (or (:data agents) [])
        agents-loading?                       (= :loading (:status agents))
        file-input-ref                        (react/useRef nil)
        _  (react/useEffect
            (fn []
              (rf/dispatch [:agents->get-agents])
              js/undefined)
            #js [])
        _  (react/useEffect
            (fn []
              (when (and (empty? agent-id) (seq agents-data))
                (set-agent-id (:id (first agents-data))))
              js/undefined)
            #js [agents-data agent-id])
        resource-names (set (map :name resources))
        cfg            configs
        resources-by-name (into {} (map (juxt :name identity) resources))
        handle-csv!    (fn [file]
                         (shared/parse-csv!
                          file
                          {:on-complete
                           (fn [rows]
                             (let [by-name     (rows->admin-credentials rows)
                                   matched     (select-keys by-name resource-names)
                                   match-count (count matched)]
                               (set-csv-row-count (count rows))
                               (set-csv-match-count match-count)
                               (set-csv-parsed true)
                               (set-configs
                                (fn [old-cfg]
                                  (reduce-kv
                                   (fn [acc res-name creds]
                                     (if-let [r (get resources-by-name res-name)]
                                       (assoc acc (:id r) creds)
                                       acc))
                                   old-cfg
                                   matched)))))}))
        valid-configs  (filterv
                        (fn [r]
                          (let [c (get cfg (:id r))]
                            (and (seq (:username c)) (seq (:password c)))))
                        resources)
        do-apply!      (fn []
                         (set-applying true)
                         (set-apply-progress 0)
                         (let [queue (mapv (fn [r]
                                            (let [c (get cfg (:id r))]
                                              {:resource-name (:name r)
                                               :username      (:username c)
                                               :password      (:password c)}))
                                          valid-configs)]
                           (rf/dispatch
                            [:provisioning/apply-admin-next
                             {:queue       queue
                              :agent-id    agent-id
                              :on-progress (fn [done total]
                                             (set-apply-progress
                                              (js/Math.round (* 100 (/ done total)))))
                              :on-complete (fn [results]
                                             (let [ok   (count (filter #(= :success (:status %)) results))
                                                   fail (count (filter #(= :failed (:status %)) results))]
                                               (set-apply-results {:succeeded ok :failed fail})
                                               (set-apply-progress 100)
                                               (rf/dispatch [:provisioning/fetch-resources])))}])))]
    [:> Flex {:direction "column" :style {:flex 1 :min-height 0}}
     [shared/bulk-screen-header {:title          "Manage \u2014 admin accounts"
                                 :resource-count (count resources)
                                 :on-back        on-cancel}]

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
       (if agents-loading?
         [:> Text {:size "1" :color "gray"} "Loading agents\u2026"]
         [:> Select.Root {:size "1" :value agent-id
                          :onValueChange set-agent-id}
          [:> Select.Trigger {:style {:width 240}}]
          [:> Select.Content
           (doall
            (for [a agents-data]
              ^{:key (:id a)}
              [:> Select.Item {:value (:id a)}
               (str (:name a)
                    (when (= "CONNECTED" (:status a)) " \u2014 online"))]))]])]]

     ;; Applying progress overlay
     (when applying?
       (if apply-results
         [:> Flex {:direction "column" :align "center" :justify "center"
                   :style {:flex 1} :gap "5"}
          [:> Box {:style {:color (if (zero? (:failed apply-results))
                                   "var(--green-9)" "var(--amber-9)")
                           :display "flex"}}
           (if (zero? (:failed apply-results))
             [:> CheckCircle2 {:size 48 :stroke-width 1.5}]
             [:> AlertCircle {:size 48 :stroke-width 1.5}])]
          [:> Heading {:size "6"} "Admin setup complete"]
          [:> Flex {:direction "column" :gap "1" :align "center"}
           [:> Text {:size "2" :color "green"}
            (str (:succeeded apply-results) " resources updated")]
           (when (pos? (:failed apply-results))
             [:> Text {:size "2" :color "red"}
              (str (:failed apply-results) " failed")])]
          [:> Button {:on-click (or on-done on-cancel)} "Continue to provision"]]

         [:> Flex {:direction "column" :align "center" :justify "center"
                   :style {:flex 1} :gap "5"}
          [:> Flex {:direction "column" :align "center" :gap "4" :style {:width 400}}
           [:> Flex {:align "center" :gap "2"}
            [shared/spinner {:color "indigo" :size 20}]
            [:> Text {:size "3" :weight "medium"}
             (str "Setting admin credentials\u2026 ("
                  (js/Math.round (/ (* apply-progress (count valid-configs)) 100))
                  " of " (count valid-configs) ")")]]
           [:> Box {:style {:width "100%"}}
            [:> Progress {:value apply-progress :size "2" :color "indigo"}]]
           [:> Text {:size "2" :color "gray"}
            (str (js/Math.round apply-progress) "% complete")]]]))

     (when-not applying?
       [:<>
        ;; Hidden file input
        [:input {:type "file"
                 :accept ".csv"
                 :ref #(set! (.-current file-input-ref) %)
                 :style {:display "none"}
                 :on-change (fn [e]
                              (when-let [file (-> e .-target .-files (aget 0))]
                                (handle-csv! file)))}]

        ;; Mode toggle
        [:> Flex {:gap "2" :mb "4"}
         [:> Button {:size "2"
                     :variant (if (= mode "manual") "solid" "outline")
                     :color   (if (= mode "manual") "indigo" "gray")
                     :on-click #(set-mode "manual")}
          [:> UserCog {:size 14}] " Enter manually"]
         [:> Button {:size "2"
                     :variant (if (= mode "csv") "solid" "outline")
                     :color   (if (= mode "csv") "indigo" "gray")
                     :on-click #(set-mode "csv")}
          [:> Upload {:size 14}] " Import from CSV"]]

        ;; Manual mode
        (when (= mode "manual")
          [:<>
           [shared/flex-table-header
            [{:width 260 :label "Resource"}
             {:width 150 :label "Admin user"}
             {:flex 1    :label "Password"}]]
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
                                   :background (shared/zebra-bg i)}}
                  [:> Box {:style {:width 260 :flex-shrink 0}}
                   [:> Flex {:align "center" :gap "2"}
                    [:> Text {:size "2" :weight "medium"} (:name r)]
                    [:> Badge {:color "gray" :variant "soft" :size "1"} (:db-type r)]]
                   [:> Text {:size "1" :color "gray"
                             :style {:font-family "var(--font-mono)" :font-size 11}}
                    (:address r)]]
                  [:> Box {:style {:width 150 :flex-shrink 0}}
                   [:> TextField.Root {:size "1" :placeholder "Admin user"
                                       :value (or (:username c) "")
                                       :onChange #(set-configs
                                                 (fn [c] (assoc-in c [(:id r) :username]
                                                                   (.. % -target -value))))}]]
                  [:> Box {:style {:flex 1}}
                   (let [shown? (contains? visible-pwds (:id r))]
                     [:> TextField.Root {:size "1"
                                         :type (if shown? "text" "password")
                                         :placeholder "Password"
                                         :value (or (:password c) "")
                                         :onChange #(set-configs
                                                     (fn [c] (assoc-in c [(:id r) :password]
                                                                       (.. % -target -value))))}
                      [:> TextField.Slot {:side "right"}
                       [eye-toggle {:visible? shown?
                                    :on-click #(toggle-pwd! (:id r))}]]])]])))]])

        ;; CSV mode
        (when (= mode "csv")
          (if (not csv-parsed)
            ;; Upload area
            [:> Flex {:direction "column" :gap "3" :style {:flex 1}}
             [shared/csv-drop-zone {:on-file   handle-csv!
                                    :hint-text "Columns: name, admin_user, password"}]]

            ;; Parsed preview
            [:> Flex {:direction "column" :gap "3" :style {:flex 1 :min-height 0}}
             [:> Flex {:align "center" :gap "3" :mb "2"}
              [:> Badge {:color "green" :variant "soft"}
               (str csv-match-count " matched")]
              [:> Badge {:color "gray" :variant "soft"}
               (str csv-row-count " rows in CSV")]
              (when (< csv-match-count csv-row-count)
                [:> Badge {:color "amber" :variant "soft"}
                 (str (- csv-row-count csv-match-count) " unmatched")])
              [:> Button {:variant "ghost" :size "1" :color "gray"
                          :on-click (fn []
                                      (set-csv-parsed false)
                                      (set-configs (constantly {}))
                                      (when-let [el (.-current file-input-ref)]
                                        (set! (.-value el) "")))}
               [:> X {:size 11}] " Clear"]]

             (when (zero? csv-match-count)
               [shared/info-callout
                {:color "amber" :size "1" :mb "2"
                 :icon  [:> AlertCircle {:size 14}]
                 :text  "No CSV rows matched the selected resources. Check that the 'name' column matches resource names."}])

             [:> Box {:style {:flex 1 :overflow-y "auto"
                              :border "1px solid var(--gray-5)"
                              :border-radius "var(--radius-2)"}}
              [shared/flex-table-header
               [{:flex 1    :label "Resource"}
                {:width 120 :label "Admin user"}
                {:width 180 :label "Password"}
                {:width 80  :label "Status"}]]
              (doall
               (for [[i r] (map-indexed vector resources)]
                 (let [c          (get cfg (:id r))
                       has-creds? (and (seq (:username c)) (seq (:password c)))]
                   ^{:key (:id r)}
                   [:> Flex {:px "3" :py "2" :align "center"
                             :style {:border-bottom (when (< i (dec (count resources)))
                                                      "1px solid var(--gray-3)")
                                     :min-height 44
                                     :background (if has-creds?
                                                   "var(--green-1)"
                                                   (shared/zebra-bg i))}}
                    [:> Flex {:align "center" :gap "2" :style {:flex 1}}
                     [:> Text {:size "2" :weight "medium"} (:name r)]
                     [:> Badge {:color "gray" :variant "soft" :size "1"} (:db-type r)]]
                    [:> Box {:style {:width 120 :flex-shrink 0}}
                     (if has-creds?
                       [:> Text {:size "2"} (:username c)]
                       [:> Text {:size "1" :color "gray" :style {:font-style "italic"}} "no match"])]
                    [:> Box {:style {:width 180 :flex-shrink 0}}
                     (when has-creds?
                       [:> Text {:size "1" :color "gray"
                                 :style {:font-family "var(--font-mono)" :font-size 11
                                         :overflow "hidden" :text-overflow "ellipsis"
                                         :white-space "nowrap"}}
                        (mask-password (:password c))])]
                    [:> Box {:style {:width 80}}
                     (if has-creds?
                       [:> Badge {:color "green" :variant "soft" :size "1"}
                        [:> Check {:size 10}] " Matched"]
                       [:> Badge {:color "gray" :variant "soft" :size "1"} "Missing"])]])))]]))

        ;; Footer
        [shared/bulk-footer
         {:info-text       (str (count valid-configs) " of " (count resources) " resources have credentials")
          :on-cancel       on-cancel
          :apply-disabled? (zero? (count valid-configs))
          :apply-label     (str "Apply to " (data/pluralize (count valid-configs) "resource")
                                " \u2192")
          :on-apply        do-apply!}]])]))

(defn bulk-admin-screen
  [props]
  [:f> bulk-admin-screen-inner props])
