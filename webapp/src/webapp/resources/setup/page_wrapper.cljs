(ns webapp.resources.setup.page-wrapper
  (:require
   ["@radix-ui/themes" :refer [Avatar Button Box Badge Flex Text]]
   ["lucide-react" :refer [PackagePlus Check BrainCog UserRoundCheck]]
   [re-frame.core :as rf]
   [reagent.core :as r]))

(defn stepper-item [{:keys [title description completed? active? icon]}]
  [:> Flex {:gap "3"
            :align "center"
            :justify "between"
            :class "w-full p-radix-3 bg-white rounded-5 border border-gray-4"}
   [:> Flex {:gap "3" :align "center"}
    (if active?
      [:> Avatar {:size "4"
                  :variant "soft"
                  :color "green"
                  :fallback (r/as-element [icon "green"])}]

      [:> Avatar {:size "4"
                  :variant "soft"
                  :color "gray"
                  :fallback (r/as-element [icon "gray"])}])
    [:> Box
     [:> Text {:as "p"
               :size "3"
               :weight (if active? "medium" "regular")
               :class (cond
                        active? "text-gray-12"
                        completed? "text-gray-11"
                        :else "text-gray-11")}
      title]
     (when (and (not completed?)
                description)
       [:> Text {:as "p"
                 :size "2"
                 :class (cond
                          active? "text-gray-11"
                          completed? "text-gray-10"
                          :else "text-gray-10")}
        description])]]

   (when completed?
     [:> Box
      [:> Avatar {:size "1"
                  :radius "full"
                  :variant "soft"
                  :color "green"
                  :fallback (r/as-element
                             [:> Check {:size 16 :color "green"}])}]])])

(defn stepper-connector [{:keys [show-next?]}]
  (if show-next?
    [:> Flex {:direction "column" :pl "6"}
     [:> Box {:class "w-[2px] h-6 bg-gray-6"}]
     [:> Badge {:size "1" :variant "soft" :color "gray" :radius "full" :class "-ml-[18px] w-fit"}
      "Next"]
     [:> Box {:class "w-[2px] h-6 bg-gray-6"}]]

    [:> Box {:pl "6"}
     [:> Box {:class "w-[2px] h-6 bg-gray-6"}]]))

(defn stepper []
  (let [current-step @(rf/subscribe [:resource-setup/current-step])]
    [:> Flex {:direction "column" :class "w-[350px] pt-24 px-10 pb-10 bg-gray-1"}
     [stepper-item {:title "Resource type"
                    :description "Quickly add your own services and databases or try a demo setup."
                    :icon (fn [color]
                            [:> PackagePlus {:size 18 :color color}])
                    :completed? (or (= current-step :agent-selector)
                                    (= current-step :roles))
                    :active? (= current-step :resource-name)}]

     [stepper-connector {:show-next? (= current-step :resource-name)}]

     [stepper-item {:title "Setup Agents"
                    :description "Establish secure communication with your infrastructure."
                    :icon (fn [color]
                            [:> BrainCog {:size 18 :color color}])
                    :completed? (= current-step :roles)
                    :active? (= current-step :agent-selector)}]

     [stepper-connector {:show-next? (= current-step :agent-selector)}]

     [stepper-item {:title "Resource roles"
                    :description "Configure permissions and usage details for your resource."
                    :icon (fn [color]
                            [:> UserRoundCheck {:size 18 :color color}])
                    :completed? false
                    :active? (= current-step :roles)}]]))

(defn footer-nav
  "Wizard navigation footer. Flows at the end of the scrollable content instead of
  being pinned to the viewport, so the primary action (Next / Save and Finish) only
  becomes visible once the user has scrolled past every field — removing the
  \"false bottom\" that let people submit incomplete forms (EVL-103)."
  [{:keys [hide-footer? on-cancel next-hidden? next-disabled? on-next next-text]}]
  (when-not hide-footer?
    [:> Flex {:justify "end"
              :align "center"
              :class "border-t border-gray-6 p-6 bg-white flex-shrink-0"}
     [:> Flex {:gap "5" :align "center"}
      (when on-cancel
        [:> Button {:size "2"
                    :variant "ghost"
                    :color "gray"
                    :on-click on-cancel}
         "Back"])

      (when-not next-hidden?
        [:> Button {:size "2"
                    :disabled next-disabled?
                    :on-click on-next}
         (or next-text "Next")])]]))

(defn main [{:keys [children footer-props onboarding?]}]
  (let [current-step @(rf/subscribe [:resource-setup/current-step])
        show-stepper? (and onboarding? (not= current-step :success))]
    (if onboarding?
      ;; Onboarding layout: stepper on the left + form on the right. The whole
      ;; area scrolls as one document so the full-width footer flows at the true
      ;; end — spanning edge-to-edge, not confined to the content column.
      ;; The height mirrors the shell-viewport convention (see tailwind.config.js
      ;; `height.screen`) minus this layout's own 72px top bar; the offset
      ;; variable self-neutralises outside the React shell.
      [:> Box {:class "relative bg-gray-1 overflow-y-auto"
               :style {:height "calc(100vh - var(--app-shell-header-offset, 0rem) - 72px)"}}
       [:> Flex {:direction "column" :class "min-h-full"}
        ;; grow => the row fills the viewport on short steps;
        ;; shrink-0 => tall content is never clipped, the whole area scrolls.
        [:> Flex {:class "grow shrink-0"}
         ;; Stepper on the left (only if not success step)
         (when show-stepper?
           [stepper])
         [:> Box {:class (if show-stepper? "flex-1" "w-full")}
          children]]
        [footer-nav footer-props]]]

      ;; Normal layout: full-width without stepper. The area scrolls and the
      ;; footer flows at the end. `h-screen` already discounts the React shell's
      ;; global header (see tailwind.config.js `height.screen`), so the footer is
      ;; never pushed below the fold.
      ;; `relative` is required, not cosmetic: it makes this box the containing
      ;; block for absolutely positioned descendants (e.g. react-select's
      ;; off-screen a11y text in the Attributes field). Without it they resolve
      ;; against the Radix root, escape this scroll container and extend the
      ;; document, producing a second scrollbar that drags the in-flow footer
      ;; into the middle of the viewport. The Radix ScrollArea this replaced gave
      ;; the same guarantee via its relatively positioned viewport.
      [:> Box {:class "relative h-screen overflow-y-auto bg-gray-1"}
       [:> Flex {:direction "column" :class "min-h-full"}
        [:> Box {:class "grow shrink-0"}
         children]
        [footer-nav footer-props]]])))
