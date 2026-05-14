(ns webapp.features.rulepacks.views.roles-tab
  (:require
   ["@radix-ui/themes" :refer [Box Button Checkbox Flex Popover Table Text TextField]]
   ["lucide-react" :refer [Check ListVideo Rotate3d Search Shapes Tag X]]
   [clojure.string :as str]
   [re-frame.core :as r-f]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]))

(defn- value-filter
  "Single-select Popover filter parameterized by icon, label, and option list.
   - :icon       Lucide icon component
   - :label      Default label when nothing is selected
   - :values     Vector of strings (the available options)
   - :selected   Currently selected value (string or nil)
   - :on-select  Fn called with the chosen value
   - :on-clear   Fn called with no args"
  [_]
  (let [open? (r/atom false)
        search-term (r/atom "")]
    (fn [{:keys [icon label values selected on-select on-clear]}]
      (let [has-selected? (and (string? selected) (not (str/blank? selected)))
            q (str/lower-case (str/trim @search-term))
            filtered (if (str/blank? q)
                       values
                       (filterv #(str/includes? (str/lower-case %) q) values))
            close! #(do (reset! open? false)
                        (reset! search-term ""))]
        [:> Popover.Root {:open @open?
                          :on-open-change #(reset! open? %)}
         [:> Popover.Trigger {:asChild true}
          [:> Button {:variant (if has-selected? "soft" "outline")
                      :color "gray"
                      :size "3"
                      :class "gap-3 h-10 px-4 font-medium text-[--gray-10]"}
           [:> icon {:size 18}]
           [:> Text {:size "3" :weight "medium"}
            (if has-selected? selected label)]
           (when has-selected?
             [:> X {:size 14
                    :on-click (fn [e]
                                (.stopPropagation e)
                                (on-clear)
                                (close!))}])]]
         [:> Popover.Content {:size "2" :style {:width "320px" :max-height "400px"}}
          [:> Box
           (when has-selected?
             [:> Box {:mb "2" :pb "2" :class "border-b border-[--gray-a6]"}
              [:> Flex {:align "center" :gap "2"
                        :class "cursor-pointer text-[--gray-11] hover:bg-[--gray-a3] rounded px-3 py-2"
                        :on-click (fn []
                                    (on-clear)
                                    (close!))}
               [:> Text {:size "2"} "Clear filter"]]])
           [:> Box {:mb "2"}
            [:> TextField.Root {:placeholder (str "Search " (str/lower-case label))
                                :value @search-term
                                :on-change #(reset! search-term (.. % -target -value))}
             [:> TextField.Slot
              [:> Search {:size 14}]]]]
           (if (seq filtered)
             [:> Box {:class "max-h-72 overflow-y-auto"}
              (doall
               (for [v filtered]
                 ^{:key v}
                 [:> Flex {:align "center" :justify "between" :gap "2"
                           :class "cursor-pointer hover:bg-[--gray-a3] rounded px-3 py-2"
                           :on-click (fn []
                                       (on-select v)
                                       (close!))}
                  [:> Text {:size "2" :class "truncate"} v]
                  (when (= v selected)
                    [:> Check {:size 14}])]))]
             [:> Box {:px "3" :py "4"}
              [:> Text {:size "1" :class "text-[--gray-11] italic"}
               (if (seq @search-term)
                 (str "No " (str/lower-case label) " found")
                 (str "No " (str/lower-case label) " available"))]])]]]))))

(defn- distinct-non-blank
  "Sorted distinct non-blank strings extracted from coll via f.
   f may return a scalar or a collection (which is flattened)."
  [coll f]
  (->> coll
       (mapcat (fn [item]
                 (let [v (f item)]
                   (cond
                     (sequential? v) v
                     (nil? v) []
                     :else [v]))))
       (remove nil?)
       (map str)
       (remove str/blank?)
       distinct
       sort
       vec))

(defn- conn-matches-filters?
  [conn {:keys [resource type attribute tag]}]
  (and (or (nil? resource)
           (= resource (:resource_name conn)))
       (or (nil? type)
           (= type (or (:subtype conn) (:type conn))))
       (or (nil? attribute)
           (some #(= attribute %) (or (:attributes conn) [])))
       (or (nil? tag)
           (some #(= tag %) (or (:_tags conn) [])))))

(defn- connection-row [{:keys [conn checked? on-toggle]}]
  [:> Table.Row
   [:> Table.Cell
    [:> Flex {:gap "2" :align "center"}
     [:> Checkbox {:checked checked?
                   :on-checked-change on-toggle}]
     [:> Text {:size "2" :class "text-[--gray-12]"}
      (:name conn)]]]
   [:> Table.Cell
    [:> Text {:size "2" :class "text-[--gray-12]"}
     (or (:subtype conn) (:type conn) "")]]
   [:> Table.Cell
    [:> Text {:size "2" :class "text-[--gray-12]"}
     (or (:resource_name conn) "")]]])

(defn main []
  (r-f/dispatch [:rulepacks/fetch-connections])
  (let [search (r/atom "")
        filters (r/atom {:resource nil :type nil :attribute nil :tag nil})]
    (fn []
      (let [connections @(r-f/subscribe [:rulepacks/connections])
            status @(r-f/subscribe [:rulepacks/connections-status])
            selected @(r-f/subscribe [:rulepacks/selected-connections])
            pending? @(r-f/subscribe [:rulepacks/has-pending-changes?])
            applying? @(r-f/subscribe [:rulepacks/applying?])
            loading? (= :loading status)
            q (str/lower-case (or @search ""))
            f @filters
            resource-options (distinct-non-blank connections :resource_name)
            type-options (distinct-non-blank connections #(or (:subtype %) (:type %)))
            attribute-options (distinct-non-blank connections :attributes)
            tag-options (distinct-non-blank connections :_tags)
            any-filter? (or (seq q)
                            (some some? (vals f)))
            visible (->> connections
                         (filter #(conn-matches-filters? % f))
                         (filter #(or (str/blank? q)
                                      (str/includes? (str/lower-case (or (:name %) "")) q))))]
        [:> Flex {:direction "column" :gap "2" :class "w-full"}
         [:> Flex {:gap "2" :align "start" :wrap "wrap" :class "w-full"}
          [:> Box {:class "flex-1 min-w-0"}
           [:> TextField.Root {:value @search
                               :on-change #(reset! search (.. % -target -value))
                               :placeholder "Search roles"
                               :size "3"
                               :class "h-10"}
            [:> TextField.Slot
             [:> Search {:size 16}]]]]
          [value-filter {:icon Rotate3d :label "Resource"
                         :values resource-options
                         :selected (:resource f)
                         :on-select #(swap! filters assoc :resource %)
                         :on-clear #(swap! filters assoc :resource nil)}]
          [value-filter {:icon Shapes :label "Type"
                         :values type-options
                         :selected (:type f)
                         :on-select #(swap! filters assoc :type %)
                         :on-clear #(swap! filters assoc :type nil)}]
          [value-filter {:icon ListVideo :label "Attribute"
                         :values attribute-options
                         :selected (:attribute f)
                         :on-select #(swap! filters assoc :attribute %)
                         :on-clear #(swap! filters assoc :attribute nil)}]
          [value-filter {:icon Tag :label "Tags"
                         :values tag-options
                         :selected (:tag f)
                         :on-select #(swap! filters assoc :tag %)
                         :on-clear #(swap! filters assoc :tag nil)}]]

         [:> Box {:class "bg-white border border-[--gray-a6] rounded-3 overflow-hidden w-full"}
          (cond
            loading?
            [:> Flex {:justify "center" :align "center" :py "7"}
             [loaders/simple-loader]]

            (empty? visible)
            [:> Box {:p "7" :class "text-center"}
             [:> Text {:size "3" :class "text-[--gray-11]"}
              (if any-filter?
                "No connections match your filters."
                "No connections available.")]]

            :else
            [:> Table.Root {:variant "ghost"}
             [:> Table.Header
              [:> Table.Row {:class "bg-[--gray-a2]"}
               [:> Table.ColumnHeaderCell
                [:> Text {:size "2" :weight "bold"} "Role name"]]
               [:> Table.ColumnHeaderCell
                [:> Text {:size "2" :weight "bold"} "Type"]]
               [:> Table.ColumnHeaderCell
                [:> Text {:size "2" :weight "bold"} "Resource"]]]]
             [:> Table.Body
              (doall
               (for [conn visible]
                 ^{:key (or (:id conn) (:name conn))}
                 [connection-row {:conn conn
                                  :checked? (contains? selected (:name conn))
                                  :on-toggle #(r-f/dispatch
                                               [:rulepacks/toggle-connection (:name conn)])}]))]])]

         (when pending?
           [:> Flex {:justify "end" :gap "2" :pt "3" :class "w-full"}
            [:> Button {:variant "soft" :color "gray" :size "3"
                        :disabled applying?
                        :on-click #(r-f/dispatch [:rulepacks/reset-selected-connections])}
             "Discard"]
            [:> Button {:size "3"
                        :disabled applying?
                        :on-click #(r-f/dispatch [:rulepacks/apply-connections])}
             (if applying? "Applying..." "Apply changes")]])]))))
